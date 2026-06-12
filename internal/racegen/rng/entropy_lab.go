//go:build gli_lab

package rng

import (
	"crypto/hkdf"
	"crypto/sha256"
	"fmt"
)

// LabEntropy es la EntropySource del MODO LABORATORIO (build tag gli_lab):
// un expansor determinista HKDF-SHA256 de una semilla de 32 bytes. Cada
// petición de entropía (instanciación y reseeds) deriva material con un
// info-string distinto (contador), de modo que la secuencia COMPLETA del
// DRBG — incluidos todos los reseeds — es función exclusiva de la semilla.
//
// Esto existe para el banco de pruebas del laboratorio (replay bit a bit
// con semilla conocida) y para los golden tests. JAMÁS se compila en el
// binario de producción: el archivo entero está tras el build tag, y la
// verificación de ausencia forma parte del procedimiento de build
// (docs/PLAN-CERTIFICACION-GLI19.md, Fase 6).
type LabEntropy struct {
	prk     []byte
	counter uint64
}

// NewLabEntropy deriva el PRK HKDF de una semilla de exactamente 32 bytes.
func NewLabEntropy(seed []byte) (*LabEntropy, error) {
	if len(seed) != 32 {
		return nil, fmt.Errorf("rng: LabEntropy seed debe ser 32 bytes, got %d", len(seed))
	}
	prk, err := hkdf.Extract(sha256.New, seed, []byte("vg-racegen/gli_lab/v1"))
	if err != nil {
		return nil, fmt.Errorf("rng: hkdf extract: %w", err)
	}
	return &LabEntropy{prk: prk}, nil
}

// Entropy implementa EntropySource de forma determinista.
func (l *LabEntropy) Entropy(p []byte) error {
	l.counter++
	info := fmt.Sprintf("entropy/%d/%d", l.counter, len(p))
	out, err := hkdf.Expand(sha256.New, l.prk, info, len(p))
	if err != nil {
		return fmt.Errorf("rng: hkdf expand: %w", err)
	}
	copy(p, out)
	return nil
}

// Describe implementa EntropySource. No revela la semilla.
func (l *LabEntropy) Describe() string { return "gli_lab:hkdf-sha256" }
