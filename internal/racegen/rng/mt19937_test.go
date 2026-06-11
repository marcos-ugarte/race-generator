package rng

import "testing"

// Test vectors oficiales MT19937 — seed 5489 (default).
// Fuente: Matsumoto & Nishimura reference implementation.
func TestMT19937KnownVectors(t *testing.T) {
	mt := NewMT19937WithUint32Seed(5489)
	want := []uint32{
		3499211612, 581869302, 3890346734, 3586334585, 545404204,
		4161255391, 3922919429, 949333985, 2715962298, 1323567403,
	}
	for i, w := range want {
		got := mt.NextUint32()
		if got != w {
			t.Fatalf("seq[%d] = %d, want %d", i, got, w)
		}
	}
}

func TestMT19937Reproducibility(t *testing.T) {
	seedHex := "0000000000000000000000000000000000000000000000000000000000000001"
	a, err := NewMT19937WithSeedHex(seedHex)
	if err != nil {
		t.Fatalf("seed hex: %v", err)
	}
	b, err := NewMT19937WithSeedHex(seedHex)
	if err != nil {
		t.Fatalf("seed hex: %v", err)
	}
	for i := 0; i < 1000; i++ {
		if a.NextUint32() != b.NextUint32() {
			t.Fatalf("divergencia en iter %d", i)
		}
	}
}

func TestMT19937SaveRestoreState(t *testing.T) {
	mt, _ := NewMT19937WithSeedHex("00000000000000000000000000000000000000000000000000000000000000ff")
	for i := 0; i < 500; i++ {
		mt.NextUint32()
	}
	snap := mt.State()
	want := make([]uint32, 100)
	for i := range want {
		want[i] = mt.NextUint32()
	}

	mt2, _ := NewMT19937WithSeedHex("00000000000000000000000000000000000000000000000000000000000000ff")
	for i := 0; i < 500; i++ {
		mt2.NextUint32()
	}
	if err := mt2.RestoreState(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	for i, w := range want {
		if g := mt2.NextUint32(); g != w {
			t.Fatalf("post-restore[%d] = %d, want %d", i, g, w)
		}
	}
}

func TestMT19937AdvanceEqualsDiscards(t *testing.T) {
	mt1, _ := NewMT19937WithSeedHex("0000000000000000000000000000000000000000000000000000000000000042")
	mt2, _ := NewMT19937WithSeedHex("0000000000000000000000000000000000000000000000000000000000000042")
	for i := 0; i < 137; i++ {
		mt1.NextUint32()
	}
	mt2.Advance(137)
	for i := 0; i < 50; i++ {
		if mt1.NextUint32() != mt2.NextUint32() {
			t.Fatalf("advance no equivale a discard en iter %d", i)
		}
	}
}
