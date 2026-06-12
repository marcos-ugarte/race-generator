//go:build !gli_lab

package main

import (
	"fmt"

	"vg-racegen/internal/racegen/rng"
)

// rngPersonalization is identical to cmd/race-generator's so the harness
// instantiates the EXACT certified production stream ("same RNG and
// methods" — GLI Composite Submission Requirements §2.2).
const rngPersonalization = "vg-racegen/race-generator/v1"

// makeSource (production flavor): HMAC-DRBG over crypto/rand; -seed rejected.
func makeSource(seedHex string) (rng.Source, string, error) {
	if seedHex != "" {
		return nil, "", fmt.Errorf("-seed is only supported in gli_lab builds (go build -tags gli_lab ./cmd/rngextract)")
	}
	src, err := rng.NewHMACDRBG(rng.CryptoEntropy{}, []byte(rngPersonalization))
	if err != nil {
		return nil, "", fmt.Errorf("hmac-drbg instantiate: %w", err)
	}
	return src, "hmac-drbg-sha256/" + rng.CryptoEntropy{}.Describe(), nil
}
