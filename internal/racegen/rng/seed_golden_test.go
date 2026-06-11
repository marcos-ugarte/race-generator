package rng

import "testing"

// TestSeedHexGoldenVector pins the EXACT first 16 uint32 produced by the
// SHA-256 cascade seeding (NewMT19937WithSeedHex) for a fixed seed. Unlike
// TestMT19937Reproducibility (which only proves two instances agree) and
// TestMT19937KnownVectors (which pins the classic uint32-seed routine), this
// pins our bespoke 32-byte→624-uint32 expansion against a frozen reference.
//
// GLI-19 §3.2/§3.3: the seeding algorithm is part of the certified surface.
// A change to the cascade (or the tempering) MUST fail this test loudly — if
// you intend the change, rebaseline these values and note it in the
// certification evidence (a seed→output remap invalidates prior golden
// fixtures and any submitted RNG report).
func TestSeedHexGoldenVector(t *testing.T) {
	const seedHex = "4242424242424242424242424242424242424242424242424242424242424242"
	want := []uint32{
		1334318904, 982030433, 313305615, 593986340,
		1886028226, 1735463223, 562007685, 1232420984,
		1630951274, 3977844139, 2418660125, 3693490342,
		1217092698, 2457497991, 243273698, 3033862261,
	}
	mt, err := NewMT19937WithSeedHex(seedHex)
	if err != nil {
		t.Fatalf("seed hex: %v", err)
	}
	for i, w := range want {
		if got := mt.NextUint32(); got != w {
			t.Fatalf("uint32[%d] = %d, want %d (seed cascade changed — rebaseline only if intended)", i, got, w)
		}
	}
}
