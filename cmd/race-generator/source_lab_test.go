//go:build gli_lab

package main

import (
	"encoding/hex"
	"strings"
	"testing"
)

// TestMakeSource_LabBuild pins the gli_lab seeding contract: a 64-hex seed
// is REQUIRED, replay is bit-exact for equal seeds, and the descriptor
// carries only a fingerprint — never the seed itself (finding H3).
func TestMakeSource_LabBuild(t *testing.T) {
	if _, _, err := makeSource(""); err == nil {
		t.Fatal("expected error: gli_lab build requires RACEGEN_SEED_HEX")
	}

	seed := hex.EncodeToString(make([]byte, 32))
	a, descA, err := makeSource(seed)
	if err != nil {
		t.Fatalf("makeSource(seed): %v", err)
	}
	b, _, err := makeSource(seed)
	if err != nil {
		t.Fatalf("makeSource(seed) #2: %v", err)
	}
	for i := 0; i < 1000; i++ {
		if va, vb := a.NextUint32(), b.NextUint32(); va != vb {
			t.Fatalf("draw %d: lab replay broken: %08x != %08x", i, va, vb)
		}
	}
	if strings.Contains(descA, seed) {
		t.Fatalf("descriptor leaks the seed: %q", descA)
	}
	if !strings.Contains(descA, "seedFP=") {
		t.Errorf("descriptor should carry a seed fingerprint, got %q", descA)
	}
}
