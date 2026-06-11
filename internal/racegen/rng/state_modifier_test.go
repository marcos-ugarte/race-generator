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
	if got := mt.GenerationCount(); got != before+uint64(mod.DiscardCount) {
		t.Fatalf("gen no avanzó lo esperado: %d → %d (discard=%d)", before, got, mod.DiscardCount)
	}
	if mod.GameID != "round-test-1" {
		t.Fatalf("gameID no se preservó: %q", mod.GameID)
	}
	if mod.Reason != "between_games" {
		t.Fatalf("reason inesperado: %q", mod.Reason)
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
