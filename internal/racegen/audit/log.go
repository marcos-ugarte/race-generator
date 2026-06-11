// Package audit escribe un audit log JSONL hash-chained de las operaciones
// del generador. Cumple GLI-19: cada entrada incluye SHA-256 de la anterior,
// haciendo imposible reescribir el pasado sin invalidar la cadena.
package audit

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Entry es una entrada del audit log.
//
// Sequence, Timestamp, PrevHash y Hash los rellena el Log al hacer Append —
// el caller sólo provee Kind y Payload.
type Entry struct {
	Sequence  uint64         `json:"seq"`
	Timestamp time.Time      `json:"ts"`
	Kind      string         `json:"kind"`
	Payload   map[string]any `json:"payload"`
	PrevHash  string         `json:"prevHash"`
	Hash      string         `json:"hash"`
}

// Log es un appender thread-safe a un archivo .jsonl con cadena de hashes.
type Log struct {
	mu       sync.Mutex
	f        *os.File
	prevHash string
	seq      uint64
}

// Open abre (o crea) el archivo de audit. Si ya existía, escanea la última
// línea para continuar la cadena (resumeChain).
//
// Modo de archivo: O_APPEND|O_CREATE|O_RDWR, 0640. JSONL append-only.
func Open(path string) (*Log, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		return nil, fmt.Errorf("audit open %s: %w", path, err)
	}
	l := &Log{f: f}
	if err := l.resumeChain(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return l, nil
}

// resumeChain lee la última línea del archivo (si existe) para restaurar
// prevHash y seq antes del primer Append.
func (l *Log) resumeChain() error {
	info, err := l.f.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return nil
	}
	// Leer las últimas 16 KiB para encontrar la última línea sin levantar todo.
	const tailSize = 16 * 1024
	off := info.Size() - tailSize
	if off < 0 {
		off = 0
	}
	buf := make([]byte, info.Size()-off)
	if _, err := l.f.ReadAt(buf, off); err != nil {
		return err
	}
	// Última línea no vacía.
	end := len(buf)
	for end > 0 && (buf[end-1] == '\n' || buf[end-1] == '\r') {
		end--
	}
	start := end
	for start > 0 && buf[start-1] != '\n' {
		start--
	}
	if start == end {
		return nil
	}
	var last Entry
	if err := json.Unmarshal(buf[start:end], &last); err != nil {
		return fmt.Errorf("audit: parsear última línea: %w", err)
	}
	l.prevHash = last.Hash
	l.seq = last.Sequence
	return nil
}

// Append escribe una nueva entrada y avanza la cadena.
//
// El servidor rellena Sequence, Timestamp (UTC), PrevHash y Hash sobre el
// Entry recibido — los valores que pueda traer el caller en esos campos se
// ignoran y sobrescriben.
func (l *Log) Append(e Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.f == nil {
		return fmt.Errorf("audit: log cerrado")
	}

	l.seq++
	e.Sequence = l.seq
	e.Timestamp = time.Now().UTC()
	e.PrevHash = l.prevHash

	hash, err := computeHash(e)
	if err != nil {
		return err
	}
	e.Hash = hash

	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("audit marshal entry: %w", err)
	}
	line = append(line, '\n')
	if _, err := l.f.Write(line); err != nil {
		return fmt.Errorf("audit write: %w", err)
	}
	l.prevHash = e.Hash
	return nil
}

// Close cierra el archivo. Seguro de llamar varias veces.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// computeHash calcula SHA-256 del JSON canónico de la entrada SIN el campo
// Hash. Centralizado para que Append y Verify lo compartan exactamente.
func computeHash(e Entry) (string, error) {
	stamp := struct {
		Sequence  uint64         `json:"seq"`
		Timestamp time.Time      `json:"ts"`
		Kind      string         `json:"kind"`
		Payload   map[string]any `json:"payload"`
		PrevHash  string         `json:"prevHash"`
	}{e.Sequence, e.Timestamp, e.Kind, e.Payload, e.PrevHash}
	raw, err := json.Marshal(stamp)
	if err != nil {
		return "", fmt.Errorf("audit marshal stamp: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// Verify lee el archivo línea por línea y valida la cadena completa.
// Devuelve el número de entradas verificadas y, si la cadena está corrupta,
// un error indicando la sequence problemática.
//
// Chequeos:
//   - Hash recomputado coincide con el almacenado.
//   - PrevHash de cada entrada coincide con Hash de la anterior.
//   - Genesis (primera entrada) tiene PrevHash == "".
//   - Sequence es monotónico empezando en 1.
func Verify(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("audit verify open: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Permitir líneas grandes (payloads con muchos campos).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		prevHash string
		prevSeq  uint64
		count    int
	)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return count, fmt.Errorf("audit verify: línea %d JSON inválido: %w", count+1, err)
		}
		// Sequence debe ser monotónico (1, 2, 3, …).
		if e.Sequence != prevSeq+1 {
			return count, fmt.Errorf("audit verify: sequence rota en línea %d — esperaba %d, got %d", count+1, prevSeq+1, e.Sequence)
		}
		// PrevHash debe casar con el hash anterior.
		if e.PrevHash != prevHash {
			return count, fmt.Errorf("audit verify: prevHash roto en seq %d — esperaba %q, got %q", e.Sequence, prevHash, e.PrevHash)
		}
		// Hash recomputado debe coincidir.
		want, err := computeHash(e)
		if err != nil {
			return count, fmt.Errorf("audit verify: recompute hash seq %d: %w", e.Sequence, err)
		}
		if want != e.Hash {
			return count, fmt.Errorf("audit verify: hash inválido en seq %d — recomputado %q, almacenado %q", e.Sequence, want, e.Hash)
		}
		prevHash = e.Hash
		prevSeq = e.Sequence
		count++
	}
	if err := scanner.Err(); err != nil {
		return count, fmt.Errorf("audit verify scan: %w", err)
	}
	return count, nil
}
