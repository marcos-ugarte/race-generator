package rng

import (
	"fmt"
	"time"
)

// StateModification describe una operación GLI-19 §3.2.6 de "background cycling":
// entre dos rondas se consume un número aleatorio de valores del Source. En
// producción esto es defensa en profundidad — la imprevisibilidad real entre
// rondas la aporta el reseed del DRBG en la frontera de ronda (ver
// cmd/race-generator generateAndPersist paso 2.b).
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

// ModifyStateBetweenGames consume entre 1 y 100 valores del Source y retorna
// el registro auditable.
//
// El discard count se extrae del PROPIO stream certificado (rejection
// sampling vía CertifiedInt). Con una fuente criptográfica la salida no
// revela el estado, y así la secuencia completa — descartes incluidos — es
// función exclusiva de la semilla en el modo laboratorio (replay bit a bit;
// el diseño anterior tiraba de crypto/rand directo y lo impedía — ver
// docs/PLAN-CERTIFICACION-GLI19.md D1.c).
func ModifyStateBetweenGames(src Source, gameID string) (*StateModification, error) {
	if src == nil {
		return nil, fmt.Errorf("rng: nil Source")
	}
	n := CertifiedInt(src, 1, 100)
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
