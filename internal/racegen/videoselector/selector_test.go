package videoselector

import (
	"fmt"
	"math"
	"testing"

	"vg-racegen/internal/racegen/config"
	"vg-racegen/internal/racegen/data"
	"vg-racegen/internal/racegen/rng"
)

const testSeedHex = "4242424242424242424242424242424242424242424242424242424242424242"

func mustMT(t *testing.T) *rng.MT19937 {
	t.Helper()
	mt, err := rng.NewMT19937WithSeedHex(testSeedHex)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return mt
}

func mustConfig(t *testing.T, gt string) config.GameTypeConfigExt {
	t.Helper()
	c, err := config.Get(gt)
	if err != nil {
		t.Fatalf("config.Get(%q): %v", gt, err)
	}
	return c
}

func mustSelector(t *testing.T, gt string) (*Selector, config.GameTypeConfigExt) {
	t.Helper()
	cfg := mustConfig(t, gt)
	pool := data.VideoPool(gt)
	if pool == nil {
		t.Fatalf("nil pool for %s", gt)
	}
	sel, err := New(pool, cfg)
	if err != nil {
		t.Fatalf("New(%s): %v", gt, err)
	}
	return sel, cfg
}

func TestVideoSelectorNew_dog8(t *testing.T) {
	sel, _ := mustSelector(t, "dog8")
	if sel.Len() != 411 {
		t.Fatalf("dog8 selector.Len()=%d, want 411", sel.Len())
	}
}

func TestVideoSelectorNew_dog6(t *testing.T) {
	sel, _ := mustSelector(t, "dog6")
	if sel.Len() != 979 {
		t.Fatalf("dog6 selector.Len()=%d, want 979", sel.Len())
	}
}

func TestVideoSelectorNewErrorOnEmpty(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	empty := &data.Pool{GameType: "dog8", NumComp: 8, Entries: nil}
	if _, err := New(empty, cfg); err == nil {
		t.Fatal("expected error on empty pool, got nil")
	}
}

func TestVideoSelectorDeterministic(t *testing.T) {
	sel, _ := mustSelector(t, "dog8")
	mtA := mustMT(t)
	mtB := mustMT(t)
	for i := 0; i < 50; i++ {
		a := sel.Select(mtA)
		b := sel.Select(mtB)
		if a.VideoID != b.VideoID {
			t.Fatalf("draw %d: a=%s b=%s", i, a.VideoID, b.VideoID)
		}
	}
}

// ipfConvergeProbe drives `trials` Select calls and asserts the empirical
// first-place distribution is within `tol` pp of the normalized
// target[r] / Σtargets[r]. Validates the architect's correction:
// IPF is scale-invariant, so dog6 (Σ≈116.18) is checked against ratios,
// not absolute %.
func ipfConvergeProbe(t *testing.T, gameType string, trials int, tol float64) {
	t.Helper()
	sel, cfg := mustSelector(t, gameType)
	mt, err := rng.NewMT19937WithSeedHex(testSeedHex)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	counts := make(map[int]int, cfg.NumberCompetitor)
	for i := 0; i < trials; i++ {
		r := sel.Select(mt)
		counts[r.Order[0]]++
	}
	var sumTarget float64
	for r := 1; r <= cfg.NumberCompetitor; r++ {
		sumTarget += cfg.TargetFirstPlace[r]
	}
	for r := 1; r <= cfg.NumberCompetitor; r++ {
		expectedFrac := cfg.TargetFirstPlace[r] / sumTarget
		empiricalFrac := float64(counts[r]) / float64(trials)
		if diff := math.Abs(empiricalFrac - expectedFrac); diff > tol {
			t.Errorf("%s r=%d: empirical=%.4f, target=%.4f (delta=%.4f, tol=%.4f)",
				gameType, r, empiricalFrac, expectedFrac, diff, tol)
		}
	}
}

func TestIPFConverges_dog8(t *testing.T) {
	// 50k samples, ±1.5pp; targets sum to ~100, so we compare against
	// the normalized target fraction (no functional difference here).
	ipfConvergeProbe(t, "dog8", 50000, 0.015)
}

func TestIPFConverges_dog6(t *testing.T) {
	// 100k samples. dog6 TargetFirstPlace sums to ~116.18 — the test
	// MUST compare against the ratio, not absolute percent.
	ipfConvergeProbe(t, "dog6", 100000, 0.015)
}

func TestIPFConvergesSecond_dog8(t *testing.T) {
	sel, cfg := mustSelector(t, "dog8")
	mt, err := rng.NewMT19937WithSeedHex(testSeedHex)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	const trials = 50000
	counts := make(map[int]int, cfg.NumberCompetitor)
	for i := 0; i < trials; i++ {
		r := sel.Select(mt)
		counts[r.Order[1]]++
	}
	var sumTarget float64
	for r := 1; r <= cfg.NumberCompetitor; r++ {
		sumTarget += cfg.TargetSecondPlace[r]
	}
	const tol = 0.015
	for r := 1; r <= cfg.NumberCompetitor; r++ {
		expectedFrac := cfg.TargetSecondPlace[r] / sumTarget
		empiricalFrac := float64(counts[r]) / float64(trials)
		if diff := math.Abs(empiricalFrac - expectedFrac); diff > tol {
			t.Errorf("dog8 second r=%d: empirical=%.4f, target=%.4f (delta=%.4f, tol=%.4f)",
				r, empiricalFrac, expectedFrac, diff, tol)
		}
	}
}

func TestVideoSelectorReturnsPoolEntry(t *testing.T) {
	sel, _ := mustSelector(t, "dog8")
	pool := data.VideoPool("dog8")
	mt := mustMT(t)
	// Build a membership set from the pool — every returned VideoID
	// must hit a real entry.
	known := make(map[string]struct{}, pool.Len())
	for i := 0; i < pool.Len(); i++ {
		known[pool.At(i).ID] = struct{}{}
	}
	const draws = 200
	for i := 0; i < draws; i++ {
		r := sel.Select(mt)
		if _, ok := known[r.VideoID]; !ok {
			t.Fatalf("draw %d: %s not in pool", i, r.VideoID)
		}
	}
}

// Defensive: independent seeds should all produce a valid pick from
// the pool — even when the cumulative table's last bucket has tiny
// probability, no draw should be out of range.
func TestVideoSelectorSelectInRangeRandomSeeds(t *testing.T) {
	sel, _ := mustSelector(t, "dog8")
	for i := 0; i < 32; i++ {
		seed := fmt.Sprintf("%064x", uint64(i)*0xdeadbeefcafebabe+1)
		mt, err := rng.NewMT19937WithSeedHex(seed)
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		r := sel.Select(mt)
		if r.VideoID == "" {
			t.Fatalf("seed %d: empty VideoID", i)
		}
		if len(r.Order) != 8 {
			t.Fatalf("seed %d: Order len=%d, want 8", i, len(r.Order))
		}
	}
}
