//go:build !gli_lab

package main

import (
	"fmt"

	"vg-racegen/internal/racegen/rng"
)

// makeSource (production flavor): the exact certified production stream via
// rng.NewOSStream — single-sourced construction shared with
// cmd/race-generator ("same RNG and methods", GLI Composite Submission
// Requirements §2.2). -seed is rejected.
func makeSource(seedHex string) (rng.CertifiedStream, string, error) {
	if seedHex != "" {
		return nil, "", fmt.Errorf("-seed is only supported in gli_lab builds (go build -tags gli_lab ./cmd/rngextract)")
	}
	return rng.NewOSStream()
}
