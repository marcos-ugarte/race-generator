package rng

import (
	"math"
	"testing"
)

func TestCertifiedFloat(t *testing.T) {
	mt, _ := NewMT19937WithSeedHex("0000000000000000000000000000000000000000000000000000000000000001")
	for i := 0; i < 1000; i++ {
		f := CertifiedFloat(mt)
		if f < 0 || f >= 1.0 {
			t.Fatalf("f fuera de [0,1): %v", f)
		}
	}
}

func TestCertifiedInt(t *testing.T) {
	mt, _ := NewMT19937WithSeedHex("0000000000000000000000000000000000000000000000000000000000000002")
	for i := 0; i < 1000; i++ {
		n := CertifiedInt(mt, 1, 8)
		if n < 1 || n > 8 {
			t.Fatalf("int fuera de [1,8]: %d", n)
		}
	}
}

func TestCertifiedIntDistributesBoundaries(t *testing.T) {
	mt, _ := NewMT19937WithSeedHex("0000000000000000000000000000000000000000000000000000000000000003")
	counts := map[int]int{}
	for i := 0; i < 10000; i++ {
		counts[CertifiedInt(mt, 1, 6)]++
	}
	for k := 1; k <= 6; k++ {
		if counts[k] < 1000 || counts[k] > 2500 {
			t.Fatalf("bucket %d desbalanceado: %d (esperaba ~1667 con tolerancia)", k, counts[k])
		}
	}
}

func TestCertifiedShuffleDeterministic(t *testing.T) {
	mt1, _ := NewMT19937WithSeedHex("00000000000000000000000000000000000000000000000000000000000000aa")
	mt2, _ := NewMT19937WithSeedHex("00000000000000000000000000000000000000000000000000000000000000aa")
	a := []int{1, 2, 3, 4, 5, 6, 7, 8}
	b := []int{1, 2, 3, 4, 5, 6, 7, 8}
	CertifiedShuffle(mt1, a)
	CertifiedShuffle(mt2, b)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("shuffle no determinista en idx %d: %d vs %d", i, a[i], b[i])
		}
	}
}

func TestCertifiedNormalClampedStaysInRange(t *testing.T) {
	mt, _ := NewMT19937WithSeedHex("0000000000000000000000000000000000000000000000000000000000000004")
	for i := 0; i < 1000; i++ {
		v := CertifiedNormalClamped(mt, 10, 3, 5, 15)
		if v < 5 || v > 15 {
			t.Fatalf("clamped fuera de [5,15]: %v", v)
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("valor inválido: %v", v)
		}
	}
}
