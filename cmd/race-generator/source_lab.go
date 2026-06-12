//go:build gli_lab

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"vg-racegen/internal/racegen/rng"
)

// makeSource (build de LABORATORIO, -tags gli_lab) construye el MISMO
// HMAC-DRBG SHA-256 de producción, pero con la EntropySource determinista
// (HKDF de RACEGEN_SEED_HEX) para replay bit a bit en el banco de pruebas
// del laboratorio. La semilla es OBLIGATORIA en este build.
//
// El descriptor devuelto identifica la corrida con el SHA-256 de la semilla
// (fingerprint), nunca con la semilla misma — el audit log no debe contener
// material reproductor (hallazgo H3, docs/AUDITORIA-RNG-GLI19.md).
func makeSource(seedHex string) (rng.Source, string, error) {
	if seedHex == "" {
		return nil, "", fmt.Errorf("RACEGEN_SEED_HEX is required in gli_lab builds (deterministic lab replay)")
	}
	seed, err := hex.DecodeString(seedHex)
	if err != nil || len(seed) != 32 {
		return nil, "", fmt.Errorf("RACEGEN_SEED_HEX must be 64 hex chars (32 bytes)")
	}
	le, err := rng.NewLabEntropy(seed)
	if err != nil {
		return nil, "", fmt.Errorf("lab entropy: %w", err)
	}
	src, err := rng.NewHMACDRBG(le, []byte(rngPersonalization))
	if err != nil {
		return nil, "", fmt.Errorf("hmac-drbg instantiate: %w", err)
	}
	fp := sha256.Sum256(seed)
	desc := fmt.Sprintf("hmac-drbg-sha256/%s seedFP=%s", le.Describe(), hex.EncodeToString(fp[:])[:16])
	return src, desc, nil
}
