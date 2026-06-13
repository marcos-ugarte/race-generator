package rng

import "testing"

func TestStateModifierAdvancesRNG(t *testing.T) {
	mt, _ := NewMT19937WithSeedHex("0000000000000000000000000000000000000000000000000000000000000099")
	before := mt.GenerationCount()
	mod, err := ModifyStateBetweenGames(mt, "round-test-1")
	if err != nil {
		t.Fatalf("modify: %v", err)
	}
	if mod.DiscardCount < 1 || mod.DiscardCount > 100 {
		t.Fatalf("discard fuera de [1,100]: %d", mod.DiscardCount)
	}
	// El descarte (GenAfter-GenBefore) es exactamente DiscardCount; la
	// extracción del conteo (CertifiedInt sobre el stream) consume ≥1 draw
	// adicional ANTES de GenBefore — determinista dada la semilla.
	if got := mod.GenAfter - mod.GenBefore; got != uint64(mod.DiscardCount) {
		t.Fatalf("descarte registrado: GenAfter-GenBefore=%d, want %d", got, mod.DiscardCount)
	}
	if mod.GenBefore <= before {
		t.Fatal("la extracción del discard count debe consumir el stream certificado (GenBefore > before)")
	}
	if got := mt.GenerationCount(); got != mod.GenAfter {
		t.Fatalf("GenerationCount=%d, want GenAfter=%d", got, mod.GenAfter)
	}
	if mod.GameID != "round-test-1" {
		t.Fatalf("gameID no se preservó: %q", mod.GameID)
	}
	if mod.Reason != "between_games" {
		t.Fatalf("reason inesperado: %q", mod.Reason)
	}
}

// TestStateModifierDeterministic: con la misma semilla, dos fuentes producen
// el MISMO discard count y la misma secuencia posterior — la propiedad que
// hace el background cycling reproducible en el modo laboratorio (el diseño
// anterior extraía el conteo de crypto/rand y rompía el replay).
func TestStateModifierDeterministic(t *testing.T) {
	seedHex := "00000000000000000000000000000000000000000000000000000000000000aa"
	a, _ := NewMT19937WithSeedHex(seedHex)
	b, _ := NewMT19937WithSeedHex(seedHex)
	for i := 0; i < 5; i++ {
		modA, err := ModifyStateBetweenGames(a, "round")
		if err != nil {
			t.Fatalf("ronda %d a: %v", i, err)
		}
		modB, err := ModifyStateBetweenGames(b, "round")
		if err != nil {
			t.Fatalf("ronda %d b: %v", i, err)
		}
		if modA.DiscardCount != modB.DiscardCount {
			t.Fatalf("ronda %d: discard divergente %d vs %d", i, modA.DiscardCount, modB.DiscardCount)
		}
		for j := 0; j < 50; j++ {
			if a.NextUint32() != b.NextUint32() {
				t.Fatalf("ronda %d iter %d: secuencia divergente", i, j)
			}
		}
	}
}

// TestStateModifierDoesNotBreakReproducibility verifica que dos MT19937 con
// la misma seed, alimentados con el mismo discard count forzado vía
// modifyStateBy, siguen produciendo la misma secuencia. Esto es CRÍTICO:
// el state modifier sólo añade entropía CSPRNG al *quándo* avanzar, no al
// *cómo* — dado n fijo, debe ser determinista bit a bit.
func TestStateModifierDoesNotBreakReproducibility(t *testing.T) {
	seedHex := "00000000000000000000000000000000000000000000000000000000000000c0"
	a, err := NewMT19937WithSeedHex(seedHex)
	if err != nil {
		t.Fatalf("seed a: %v", err)
	}
	b, err := NewMT19937WithSeedHex(seedHex)
	if err != nil {
		t.Fatalf("seed b: %v", err)
	}

	// Simular 5 rondas con discards fijos pero variados.
	discards := []int{7, 42, 1, 100, 33}
	for i, n := range discards {
		modA := modifyStateBy(a, n, "round")
		modB := modifyStateBy(b, n, "round")
		if modA.DiscardCount != modB.DiscardCount {
			t.Fatalf("ronda %d: discard count divergente %d vs %d", i, modA.DiscardCount, modB.DiscardCount)
		}
		// Tras cada modifyStateBy, drenar 100 valores y comparar.
		for j := 0; j < 100; j++ {
			if a.NextUint32() != b.NextUint32() {
				t.Fatalf("ronda %d iter %d: secuencia divergente tras modifyStateBy(n=%d)", i, j, n)
			}
		}
	}

	// Sanity: GenerationCount idéntico tras todas las operaciones.
	if a.GenerationCount() != b.GenerationCount() {
		t.Fatalf("gen counts divergentes: %d vs %d", a.GenerationCount(), b.GenerationCount())
	}
}
