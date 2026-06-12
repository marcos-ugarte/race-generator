// Package rng implementa la fuente de aleatoriedad certificada GLI-19 del
// generador. La fuente de PRODUCCIÓN es el HMAC-DRBG SHA-256 (hmac_drbg.go,
// SP 800-90A) sembrado de crypto/rand.
//
// MT19937 (este archivo) según Matsumoto & Nishimura (1997), período
// 2^19937-1, espejo funcional de virteon-platform MersenneTwister.ts.
// Se conserva ÚNICAMENTE como Source determinista para tests (golden
// vectors de paridad con el legacy TS) — NO es apto como fuente de
// producción bajo GLI-19 (estado recuperable de 624 salidas; hallazgo H1
// de docs/AUDITORIA-RNG-GLI19.md).
package rng

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	mtN         = 624
	mtM         = 397
	mtMatrixA   = 0x9908b0df
	mtUpperMask = 0x80000000
	mtLowerMask = 0x7fffffff
)

// MT19937 es un PRNG Mersenne Twister.
// NO es thread-safe. El generador llama secuencialmente; si en el futuro
// hace falta concurrencia, envolverlo con sync.Mutex en la capa cliente.
type MT19937 struct {
	state [mtN]uint32
	index int
	gen   uint64 // contador monotónico de NextUint32 emitidos
}

// NewMT19937WithUint32Seed crea un MT19937 con la rutina de seed clásica
// (Matsumoto & Nishimura, 2002 update). Útil para test vectors.
func NewMT19937WithUint32Seed(seed uint32) *MT19937 {
	mt := &MT19937{}
	mt.state[0] = seed
	for i := 1; i < mtN; i++ {
		prev := mt.state[i-1]
		mt.state[i] = uint32(1812433253)*(prev^(prev>>30)) + uint32(i)
	}
	mt.index = mtN
	return mt
}

// NewMT19937WithSeedHex crea un MT19937 sembrado con 256 bits de entropía
// proveniente del hex de 64 caracteres. Usado para reproducibilidad y para
// auditoría GLI-19.
//
// Expansión: 32 bytes → 624 uint32 vía SHA-256 en cascada (key-stream).
func NewMT19937WithSeedHex(seedHex string) (*MT19937, error) {
	if len(seedHex) != 64 {
		return nil, fmt.Errorf("seedHex debe ser 64 chars hex, got %d", len(seedHex))
	}
	raw, err := hex.DecodeString(seedHex)
	if err != nil {
		return nil, fmt.Errorf("seedHex inválido: %w", err)
	}
	mt := &MT19937{}
	block := raw
	for i := 0; i < mtN; i += 8 {
		h := sha256.Sum256(block)
		block = h[:]
		for j := 0; j < 8 && i+j < mtN; j++ {
			mt.state[i+j] = binary.BigEndian.Uint32(h[j*4 : j*4+4])
			if mt.state[i+j] == 0 {
				mt.state[i+j] = 1 // evita el degenerate all-zero state
			}
		}
	}
	mt.index = mtN
	return mt, nil
}

// NextUint32 emite un uint32 uniforme [0, 2^32-1].
func (mt *MT19937) NextUint32() uint32 {
	if mt.index >= mtN {
		mt.twist()
	}
	y := mt.state[mt.index]
	mt.index++
	// Tempering canónico Matsumoto-Nishimura (shifts 11, 7, 15, 18).
	y ^= y >> 11
	y ^= (y << 7) & 0x9d2c5680
	y ^= (y << 15) & 0xefc60000
	y ^= y >> 18
	mt.gen++
	return y
}

// NextFloat emite un float64 uniforme en [0, 1).
func (mt *MT19937) NextFloat() float64 {
	return float64(mt.NextUint32()) / 4294967296.0
}

// Advance descarta n valores (GLI-19 §3.2.6 background cycling).
func (mt *MT19937) Advance(n int) {
	for i := 0; i < n; i++ {
		_ = mt.NextUint32()
	}
}

// GenerationCount devuelve cuántos uint32 se han emitido desde el seed.
func (mt *MT19937) GenerationCount() uint64 { return mt.gen }

// State retorna una copia del estado para snapshot/reproducibilidad.
type State struct {
	S   [mtN]uint32 `json:"s"`
	Idx int         `json:"idx"`
	Gen uint64      `json:"gen"`
}

// State devuelve una copia del estado interno (independiente del MT19937).
func (mt *MT19937) State() State {
	return State{S: mt.state, Idx: mt.index, Gen: mt.gen}
}

var errStateInvalid = errors.New("estado MT19937 inválido")

// RestoreState pone el RNG en un estado previo.
func (mt *MT19937) RestoreState(s State) error {
	if s.Idx < 0 || s.Idx > mtN {
		return errStateInvalid
	}
	mt.state = s.S
	mt.index = s.Idx
	mt.gen = s.Gen
	return nil
}

// twist regenera los 624 uint32 internos.
func (mt *MT19937) twist() {
	for i := 0; i < mtN; i++ {
		x := (mt.state[i] & mtUpperMask) | (mt.state[(i+1)%mtN] & mtLowerMask)
		xa := x >> 1
		if x&1 != 0 {
			xa ^= mtMatrixA
		}
		mt.state[i] = mt.state[(i+mtM)%mtN] ^ xa
	}
	mt.index = 0
}
