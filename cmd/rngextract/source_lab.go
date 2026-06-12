//go:build gli_lab

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"vg-racegen/internal/racegen/rng"
)

// rngPersonalization is identical to cmd/race-generator's so the harness
// instantiates the EXACT certified production stream ("same RNG and
// methods" — GLI Composite Submission Requirements §2.2).
const rngPersonalization = "vg-racegen/race-generator/v1"

// makeSource (gli_lab flavor): deterministic HKDF entropy from -seed
// (64 hex, REQUIRED) — bit-exact reproducible evidence runs. The returned
// descriptor carries only a SHA-256 fingerprint of the seed.
func makeSource(seedHex string) (rng.Source, string, error) {
	if seedHex == "" {
		return nil, "", fmt.Errorf("-seed (64 hex) is required in gli_lab builds")
	}
	seed, err := hex.DecodeString(seedHex)
	if err != nil || len(seed) != 32 {
		return nil, "", fmt.Errorf("-seed must be 64 hex chars (32 bytes)")
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
