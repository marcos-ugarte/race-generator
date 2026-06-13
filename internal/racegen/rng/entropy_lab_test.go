//go:build gli_lab

package rng

import "testing"

// TestLabEntropy_Deterministic: dos LabEntropy con la misma semilla producen
// la misma secuencia de entropía y, por tanto, DRBGs bit-idénticos incluso a
// través de reseeds — la propiedad que sostiene el replay del banco de
// pruebas del laboratorio.
func TestLabEntropy_Deterministic(t *testing.T) {
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i)
	}
	mk := func() *HMACDRBG {
		le, err := NewLabEntropy(seed)
		if err != nil {
			t.Fatalf("NewLabEntropy: %v", err)
		}
		d, err := NewHMACDRBG(le, []byte("vg-racegen/race-generator/v1"))
		if err != nil {
			t.Fatalf("NewHMACDRBG: %v", err)
		}
		return d
	}
	a, b := mk(), mk()
	for i := 0; i < 1000; i++ {
		if va, vb := a.NextUint32(), b.NextUint32(); va != vb {
			t.Fatalf("draw %d: %08x != %08x", i, va, vb)
		}
	}
	// Reseed explícito (frontera de ronda) — debe seguir sincronizado.
	if err := a.Reseed([]byte("round-1")); err != nil {
		t.Fatalf("reseed a: %v", err)
	}
	if err := b.Reseed([]byte("round-1")); err != nil {
		t.Fatalf("reseed b: %v", err)
	}
	for i := 0; i < 1000; i++ {
		if va, vb := a.NextUint32(), b.NextUint32(); va != vb {
			t.Fatalf("draw %d post-reseed: %08x != %08x", i, va, vb)
		}
	}
}

// TestLabEntropy_SeedLength rechaza semillas que no sean de 32 bytes.
func TestLabEntropy_SeedLength(t *testing.T) {
	if _, err := NewLabEntropy(make([]byte, 31)); err == nil {
		t.Fatal("esperaba error con semilla de 31 bytes")
	}
}
