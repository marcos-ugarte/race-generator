// Package videoselector builds a weighted, IPF-fitted sampler over the
// embedded video pool for one game type. Each Select call draws one
// VideoFinish at random according to the post-IPF cumulative
// distribution.
//
// IPF (Iterative Proportional Fitting) runs 50 iterations of 3 steps:
//
//  1. First-place marginal: scale weights so the simulated first-place
//     distribution matches cfg.TargetFirstPlace.
//  2. Second-place marginal: same, on cfg.TargetSecondPlace.
//  3. Exacta soft-correction (factor 0.2): nudge weights so the joint
//     (1st, 2nd) distribution is closer to uniform across the N*(N-1)
//     ordered exactas.
//
// Mirrors virteon-platform/packages/game-engine/src/rng/VideoSelector.ts
// L98-L194. The architect's spec adds index-map caching so each iteration
// is O(len) rather than O(len * N).
package videoselector

import (
	"errors"
	"fmt"
	"sort"

	"vg-racegen/internal/racegen/config"
	"vg-racegen/internal/racegen/data"
	"vg-racegen/internal/racegen/rng"
)

// Result is the outcome of one Select call. Order is the canonical
// finish order from the source pool entry; positions are 1-based, so
// Order[0] is the first-place runner, Order[1] is second, and so on.
//
// IMPORTANT: Order is the shared slice from data.Pool — DO NOT mutate.
type Result struct {
	VideoID string
	Order   []int
}

// Selector holds the immutable, pre-fitted cumulative distribution for
// one game type's video pool. Construct once at boot via New; Select is
// cheap (one CertifiedFloat + binary search).
type Selector struct {
	entries    []data.VideoFinish
	cumulative []float64
	totalW     float64
}

// New builds a Selector for the given pool and config. Returns an error
// if the pool is empty, the config is malformed, or any entry has the
// wrong Order length.
//
// IPF runs at construction time. Pools are small (≤1k entries) so the
// 50 iterations finish in a few ms; amortized over a long-running
// process the cost is negligible.
func New(pool *data.Pool, cfg config.GameTypeConfigExt) (*Selector, error) {
	if pool == nil || pool.Len() == 0 {
		return nil, errors.New("videoselector: empty pool")
	}
	n := cfg.NumberCompetitor
	if n < 2 {
		return nil, fmt.Errorf("videoselector: NumberCompetitor must be ≥ 2, got %d", n)
	}
	if len(cfg.TargetFirstPlace) != n {
		return nil, fmt.Errorf("videoselector: len(TargetFirstPlace)=%d, want %d",
			len(cfg.TargetFirstPlace), n)
	}
	if len(cfg.TargetSecondPlace) != n {
		return nil, fmt.Errorf("videoselector: len(TargetSecondPlace)=%d, want %d",
			len(cfg.TargetSecondPlace), n)
	}

	// Copy entries into a private slice and sort by ID. The Phase 2
	// data.Pool already sorts on parse, but we defend against future
	// changes by re-sorting here.
	entries := make([]data.VideoFinish, pool.Len())
	for i := 0; i < pool.Len(); i++ {
		e := pool.At(i)
		if len(e.Order) != n {
			return nil, fmt.Errorf("videoselector: entry %s Order len=%d, want %d",
				e.ID, len(e.Order), n)
		}
		entries[i] = e
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })

	// Build index maps once. firstIdx[r] is the list of entry indices
	// whose first-place runner is r; same for secondIdx; exactaIdx is
	// keyed by (first, second) ordered pair.
	firstIdx := make(map[int][]int, n)
	secondIdx := make(map[int][]int, n)
	exactaIdx := make(map[[2]int][]int, n*(n-1))
	for i, e := range entries {
		first := e.Order[0]
		second := e.Order[1]
		firstIdx[first] = append(firstIdx[first], i)
		secondIdx[second] = append(secondIdx[second], i)
		key := [2]int{first, second}
		exactaIdx[key] = append(exactaIdx[key], i)
	}

	// IPF. Iterations and exacta correction factor are configurable per
	// gameType to allow per-game-type tuning against vendor reference
	// distributions. Defaults preserve legacy virteon behaviour (50 / 0.2).
	weights := make([]float64, len(entries))
	for i := range weights {
		weights[i] = 1.0
	}
	iterations := cfg.IPFIterations
	if iterations <= 0 {
		iterations = 50
	}
	exactaFactor := cfg.IPFExactaFactor
	if exactaFactor <= 0 {
		exactaFactor = 0.2
	}
	targetExacta := 100.0 / float64(n*(n-1))

	for iter := 0; iter < iterations; iter++ {
		// Step 1: first-place marginal.
		ipfStep(weights, firstIdx, cfg.TargetFirstPlace)
		// Step 2: second-place marginal.
		ipfStep(weights, secondIdx, cfg.TargetSecondPlace)
		// Step 3: exacta soft-correction.
		ipfExactaStep(weights, exactaIdx, targetExacta, exactaFactor)
	}

	// Build cumulative distribution.
	cumulative := make([]float64, len(weights))
	var sum float64
	for i, w := range weights {
		sum += w
		cumulative[i] = sum
	}
	return &Selector{
		entries:    entries,
		cumulative: cumulative,
		totalW:     sum,
	}, nil
}

// ipfStep applies one marginal IPF iteration: compute the simulated
// per-key share, then scale every contributing entry's weight by
// target[key] / simulated[key]. Matches the inner loop of
// VideoSelector.ts:127-159.
func ipfStep(weights []float64, idx map[int][]int, target map[int]float64) {
	var totalW float64
	for _, w := range weights {
		totalW += w
	}
	if totalW == 0 {
		return
	}
	// Simulated share per key.
	sim := make(map[int]float64, len(idx))
	for k, members := range idx {
		var s float64
		for _, i := range members {
			s += weights[i]
		}
		sim[k] = s / totalW * 100
	}
	// Apply scaling.
	for k, members := range idx {
		t, ok := target[k]
		if !ok {
			continue
		}
		s := sim[k]
		if s <= 0 {
			continue
		}
		f := t / s
		for _, i := range members {
			weights[i] *= f
		}
	}
}

// ipfExactaStep applies the softer 3rd-step correction: nudge towards a
// uniform-exacta target by `factor` (0..1; 0 = no correction, 1 = full
// correction). Legacy default is 0.2 (20% nudge), per VideoSelector.ts:161-181.
func ipfExactaStep(weights []float64, idx map[[2]int][]int, targetExacta, factor float64) {
	var totalW float64
	for _, w := range weights {
		totalW += w
	}
	if totalW == 0 {
		return
	}
	sim := make(map[[2]int]float64, len(idx))
	for k, members := range idx {
		var s float64
		for _, i := range members {
			s += weights[i]
		}
		sim[k] = s / totalW * 100
	}
	for k, members := range idx {
		s := sim[k]
		if s <= 0 {
			continue
		}
		correction := targetExacta / s
		mult := 1 + factor*(correction-1)
		for _, i := range members {
			weights[i] *= mult
		}
	}
}

// Select draws one Result. r = CertifiedFloat * totalW, then binary
// search for the smallest cumulative >= r.
func (s *Selector) Select(mt *rng.MT19937) Result {
	r := rng.CertifiedFloat(mt) * s.totalW
	// sort.Search returns the smallest index i for which
	// s.cumulative[i] >= r. CertifiedFloat ∈ [0, 1) so r ∈ [0, totalW)
	// and i is always in range.
	i := sort.Search(len(s.cumulative), func(k int) bool {
		return s.cumulative[k] >= r
	})
	if i >= len(s.entries) {
		i = len(s.entries) - 1 // pathological; defensive
	}
	e := s.entries[i]
	// Return a copy of Order so a future caller mutating it (e.g.
	// sort.Ints for display) cannot corrupt the shared pool entry and
	// silently bias all subsequent draws. Order length is always ≤8
	// (NumberCompetitor for dog8); the allocation is negligible.
	orderCopy := make([]int, len(e.Order))
	copy(orderCopy, e.Order)
	return Result{VideoID: e.ID, Order: orderCopy}
}

// Len returns the number of entries in the underlying pool. Helpful for
// tests; the cumulative table has the same length.
func (s *Selector) Len() int { return len(s.entries) }
