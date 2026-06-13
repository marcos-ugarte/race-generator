//go:build gli_lab

package main

import (
	"fmt"

	"vg-racegen/internal/racegen/rng"
)

// makeSource (gli_lab flavor): deterministic evidence runs via
// rng.NewSeededStream — single-sourced construction shared with
// cmd/race-generator. -seed (64 hex) is REQUIRED; the descriptor carries
// only a SHA-256 fingerprint of the seed.
func makeSource(seedHex string) (rng.CertifiedStream, string, error) {
	if seedHex == "" {
		return nil, "", fmt.Errorf("-seed (64 hex) is required in gli_lab builds")
	}
	src, desc, err := rng.NewSeededStream(seedHex)
	if err != nil {
		return nil, "", fmt.Errorf("-seed: %w", err)
	}
	return src, desc, nil
}
