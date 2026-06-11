// Benchmarks for the racegen RNG package.
//
// Run: go test -bench=. -benchmem ./internal/racegen/rng/
//
// Reference numbers (AMD EPYC 9354P, 2026-05-18):
//   NextUint32        ~7  ns/op    0 alloc
//   NextFloat         ~7  ns/op    0 alloc
//   CertifiedInt      ~10 ns/op    0 alloc
//
// ~2x slower than stdlib math/rand (assembly-optimised MT19937) but
// the per-round budget (50 RNG calls = 350ns) is irrelevant at any
// realistic race volume. Do NOT spend effort optimising this further
// without a measured bottleneck.

package rng_test

import (
	"testing"

	"vg-racegen/internal/racegen/rng"
)

func BenchmarkNextUint32(b *testing.B) {
	mt := rng.NewMT19937WithUint32Seed(5489)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		mt.NextUint32()
	}
}

func BenchmarkNextFloat(b *testing.B) {
	mt := rng.NewMT19937WithUint32Seed(5489)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		mt.NextFloat()
	}
}

func BenchmarkCertifiedInt_1to100(b *testing.B) {
	mt := rng.NewMT19937WithUint32Seed(5489)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rng.CertifiedInt(mt, 1, 100)
	}
}
