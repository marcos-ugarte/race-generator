//go:build gli_lab

package main

import (
	"fmt"

	"vg-racegen/internal/racegen/rng"
)

// makeSource (build de LABORATORIO, -tags gli_lab) construye el MISMO
// HMAC-DRBG de producción con la EntropySource determinista
// (rng.NewSeededStream — construcción única compartida con cmd/rngextract)
// para replay bit a bit en el banco de pruebas. La semilla es OBLIGATORIA.
//
// Alcance del replay: la SECUENCIA DE DRAWS es función exclusiva de la
// semilla. La identidad de las rondas (roundCode, slots) sigue dependiendo
// del instante de arranque — para evidencia con identidad reproducible se
// usa cmd/rngextract, que fija el slot inicial.
func makeSource(seedHex string) (rng.CertifiedStream, string, error) {
	if seedHex == "" {
		return nil, "", fmt.Errorf("RACEGEN_SEED_HEX is required in gli_lab builds (deterministic lab replay)")
	}
	src, desc, err := rng.NewSeededStream(seedHex)
	if err != nil {
		return nil, "", fmt.Errorf("RACEGEN_SEED_HEX: %w", err)
	}
	return src, desc, nil
}
