package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogAppendChainedHashes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	l, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer l.Close()

	if err := l.Append(Entry{Kind: "init", Payload: map[string]any{"seedHex": "abc"}}); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if err := l.Append(Entry{Kind: "video_selection", Payload: map[string]any{"gameId": "g1"}}); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if err := l.Append(Entry{Kind: "state_mod", Payload: map[string]any{"gameId": "g1", "discard": 42}}); err != nil {
		t.Fatalf("append 3: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := splitLines(raw)
	if len(lines) != 3 {
		t.Fatalf("esperaba 3 líneas, got %d", len(lines))
	}
	var first, second, third Entry
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatalf("parse 1: %v", err)
	}
	if err := json.Unmarshal(lines[1], &second); err != nil {
		t.Fatalf("parse 2: %v", err)
	}
	if err := json.Unmarshal(lines[2], &third); err != nil {
		t.Fatalf("parse 3: %v", err)
	}
	if first.PrevHash != "" {
		t.Fatalf("primera entrada debería tener prevHash vacío, got %q", first.PrevHash)
	}
	if second.PrevHash != first.Hash {
		t.Fatalf("cadena rota entre 1 y 2: %q vs %q", second.PrevHash, first.Hash)
	}
	if third.PrevHash != second.Hash {
		t.Fatalf("cadena rota entre 2 y 3: %q vs %q", third.PrevHash, second.Hash)
	}
	if first.Hash == second.Hash || second.Hash == third.Hash {
		t.Fatalf("hashes idénticos — la cadena no aporta entropía")
	}
	// Sequence asignada por el log.
	if first.Sequence != 1 || second.Sequence != 2 || third.Sequence != 3 {
		t.Fatalf("sequence rota: %d, %d, %d", first.Sequence, second.Sequence, third.Sequence)
	}
}

// TestLogVerifyDetectsTampering: appende 5 entradas, muta un byte en la 3ª
// línea, y verifica que Verify devuelve error citando la sequence corrupta.
func TestLogVerifyDetectsTampering(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	l, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := l.Append(Entry{Kind: "test", Payload: map[string]any{"i": i}}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Sanity: pristine file verifica OK.
	n, err := Verify(path)
	if err != nil {
		t.Fatalf("verify pristine: %v", err)
	}
	if n != 5 {
		t.Fatalf("pristine: esperaba 5 entradas, got %d", n)
	}

	// Mutar un byte del payload en la línea 3.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := splitLines(raw)
	if len(lines) != 5 {
		t.Fatalf("esperaba 5 líneas, got %d", len(lines))
	}
	// Cambiar "i":2 por "i":9 en la línea 3 (índice 2).
	tampered := strings.Replace(string(lines[2]), `"i":2`, `"i":9`, 1)
	if tampered == string(lines[2]) {
		t.Fatalf("no se pudo aplicar mutación — el payload no contenía el patrón esperado")
	}
	lines[2] = []byte(tampered)

	var out []byte
	for _, ln := range lines {
		out = append(out, ln...)
		out = append(out, '\n')
	}
	if err := os.WriteFile(path, out, 0o640); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	// Verify debe fallar y mencionar la seq 3.
	_, err = Verify(path)
	if err == nil {
		t.Fatalf("Verify aceptó archivo manipulado")
	}
	if !strings.Contains(err.Error(), "seq 3") && !strings.Contains(err.Error(), "línea 3") {
		t.Fatalf("error no menciona seq/línea 3: %v", err)
	}
}

// TestLogResumeChain: Open, append 2 entries, Close, re-Open same path, append
// a 3rd entry. Verifica que el archivo tiene 3 líneas con cadena válida.
func TestLogResumeChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	// Primera sesión: 2 entradas.
	l1, err := Open(path)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	if err := l1.Append(Entry{Kind: "a", Payload: map[string]any{"k": "v1"}}); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if err := l1.Append(Entry{Kind: "b", Payload: map[string]any{"k": "v2"}}); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if err := l1.Close(); err != nil {
		t.Fatalf("close 1: %v", err)
	}

	// Segunda sesión: reabrir y appender una 3ª.
	l2, err := Open(path)
	if err != nil {
		t.Fatalf("open 2: %v", err)
	}
	if err := l2.Append(Entry{Kind: "c", Payload: map[string]any{"k": "v3"}}); err != nil {
		t.Fatalf("append 3: %v", err)
	}
	if err := l2.Close(); err != nil {
		t.Fatalf("close 2: %v", err)
	}

	// Verificar 3 líneas y cadena íntegra.
	n, err := Verify(path)
	if err != nil {
		t.Fatalf("verify resumed: %v", err)
	}
	if n != 3 {
		t.Fatalf("esperaba 3 entradas, got %d", n)
	}

	// Sanity: la 3ª entrada debe tener Sequence=3 y prevHash apuntando a la 2ª.
	raw, _ := os.ReadFile(path)
	lines := splitLines(raw)
	if len(lines) != 3 {
		t.Fatalf("esperaba 3 líneas en disco, got %d", len(lines))
	}
	var second, third Entry
	if err := json.Unmarshal(lines[1], &second); err != nil {
		t.Fatalf("parse 2: %v", err)
	}
	if err := json.Unmarshal(lines[2], &third); err != nil {
		t.Fatalf("parse 3: %v", err)
	}
	if third.Sequence != 3 {
		t.Fatalf("seq 3 esperada, got %d", third.Sequence)
	}
	if third.PrevHash != second.Hash {
		t.Fatalf("resume no enlazó la cadena: prevHash=%q, esperaba %q", third.PrevHash, second.Hash)
	}
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			if i > start {
				out = append(out, b[start:i])
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}
