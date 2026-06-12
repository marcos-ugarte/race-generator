package rng

import (
	"crypto/rand"
	"fmt"
)

// EntropySource abastece de material de semilla al DRBG en la instanciación
// y en cada reseed. Es la ÚNICA pieza que difiere entre el build de
// producción y el build de laboratorio (gli_lab): producción lee del CSPRNG
// del sistema operativo; el laboratorio usa un expansor determinista de una
// semilla conocida para replay bit a bit. El DRBG y sus disparadores de
// reseed son idénticos en ambos builds — exactamente la separación que exige
// la sumisión GLI ("same RNG and methods", Composite Submission Requirements
// §2.2; ver docs/PLAN-CERTIFICACION-GLI19.md D1.b).
type EntropySource interface {
	// Entropy llena p con material fresco. Debe devolver error antes que
	// degradar a una fuente débil (GLI-19 fail-safe).
	Entropy(p []byte) error
	// Describe identifica la fuente para el audit log (p.ej. "crypto/rand",
	// "gli_lab:hkdf"). Nunca debe revelar material de semilla.
	Describe() string
}

// CryptoEntropy es la EntropySource de producción: lectura directa del
// CSPRNG del sistema operativo vía crypto/rand.
//
// Desde Go 1.24, crypto/rand.Read no devuelve error en los SO soportados
// (aborta el proceso si el CSPRNG del kernel es inutilizable). Mantenemos la
// propagación de error igualmente: el contrato EntropySource exige fallo
// explícito, y un futuro cambio de runtime no debe poder degradarlo en
// silencio.
type CryptoEntropy struct{}

// Entropy implementa EntropySource sobre crypto/rand.Read.
func (CryptoEntropy) Entropy(p []byte) error {
	if _, err := rand.Read(p); err != nil {
		return fmt.Errorf("crypto/rand: %w", err)
	}
	return nil
}

// Describe implementa EntropySource.
func (CryptoEntropy) Describe() string { return "crypto/rand" }
