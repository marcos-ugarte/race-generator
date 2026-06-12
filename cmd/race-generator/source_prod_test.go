//go:build !gli_lab

package main

import (
	"encoding/hex"
	"strings"
	"testing"

	"vg-racegen/internal/racegen/rng"
)

// TestMakeSource_ProductionBuild pins the production-build seeding contract:
// no seed ⇒ HMAC-DRBG over crypto/rand; any seed present ⇒ fatal error.
// Deterministic production seeding was finding H2 of
// docs/AUDITORIA-RNG-GLI19.md — it must be impossible outside gli_lab.
func TestMakeSource_ProductionBuild(t *testing.T) {
	src, desc, err := makeSource("")
	if err != nil {
		t.Fatalf("makeSource(\"\"): %v", err)
	}
	if src == nil || !strings.Contains(desc, "hmac-drbg-sha256") {
		t.Fatalf("expected hmac-drbg source, got desc=%q", desc)
	}
	if _, ok := src.(rng.Reseeder); !ok {
		t.Error("production source must support round-boundary reseed (rng.Reseeder)")
	}
	if strings.Contains(desc, "seedFP") {
		t.Errorf("production descriptor must not carry a seed fingerprint: %q", desc)
	}

	seed := hex.EncodeToString(make([]byte, 32))
	if _, _, err := makeSource(seed); err == nil {
		t.Fatal("expected error: production build must reject RACEGEN_SEED_HEX")
	}
}
