package rng

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"
)

// StateModification describe una operación GLI-19 §3.2.6 de "background cycling":
// entre dos rondas se consume un número aleatorio de valores del Source para
// que el estado no sea trivialmente continuo entre rondas.
//
// NOTA R4: el registro NO captura el estado interno del generador — sólo los
// contadores de generación antes/después. Exponer el estado interno en un
// log permitiría reconstruir el stream (inaceptable para la certificación).
type StateModification struct {
	GameID       string    `json:"gameId"`
	Reason       string    `json:"reason"` // V1 siempre "between_games"
	GenBefore    uint64    `json:"genBefore"`
	GenAfter     uint64    `json:"genAfter"`
	DiscardCount int       `json:"discardCount"` // [1,100]
	Timestamp    time.Time `json:"timestamp"`
}

// ModifyStateBetweenGames consume entre 1 y 100 valores del Source según
// crypto/rand independiente, y retorna el registro auditable.
//
// La fuente del discard count NO es el propio Source — viene de crypto/rand
// con rejection sampling para evitar sesgo de módulo.
func ModifyStateBetweenGames(src Source, gameID string) (*StateModification, error) {
	if src == nil {
		return nil, fmt.Errorf("rng: nil Source")
	}
	n, err := cryptoRandIntRange(1, 100)
	if err != nil {
		return nil, fmt.Errorf("CSPRNG: %w", err)
	}
	return modifyStateBy(src, n, gameID), nil
}

// modifyStateBy es la variante con discard count fijo, usada por tests para
// reproducir paso a paso una secuencia. NO es API pública.
func modifyStateBy(src Source, n int, gameID string) *StateModification {
	before := src.GenerationCount()
	Advance(src, n)
	return &StateModification{
		GameID:       gameID,
		Reason:       "between_games",
		GenBefore:    before,
		GenAfter:     src.GenerationCount(),
		DiscardCount: n,
		Timestamp:    time.Now().UTC(),
	}
}

// cryptoRandIntRange devuelve un entero uniforme [min, max] usando crypto/rand
// con rejection sampling (análogo a CertifiedInt pero sobre la fuente CSPRNG).
//
// Esto evita el sesgo de módulo que tendría `v % span` cuando el rango no
// divide exactamente 2^64.
func cryptoRandIntRange(min, max int) (int, error) {
	if max < min {
		return 0, fmt.Errorf("cryptoRandIntRange: max(%d) < min(%d)", max, min)
	}
	span := uint64(max - min + 1)
	if span == 0 { // (max-min+1 desbordó: todo el espacio uint64)
		buf := make([]byte, 8)
		if _, err := rand.Read(buf); err != nil {
			return 0, err
		}
		return min + int(binary.BigEndian.Uint64(buf)), nil
	}
	// limit = mayor múltiplo de span que cabe en uint64, menos 1.
	limit := ^uint64(0) - (^uint64(0) % span) - 1
	buf := make([]byte, 8)
	for {
		if _, err := rand.Read(buf); err != nil {
			return 0, err
		}
		v := binary.BigEndian.Uint64(buf)
		if v <= limit {
			return min + int(v%span), nil
		}
	}
}
