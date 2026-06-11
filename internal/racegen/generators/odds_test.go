package generators

import (
	"fmt"
	"math"
	"sort"
	"testing"

	"vg-racegen/internal/racegen/config"
	"vg-racegen/internal/racegen/rng"
)

// identityOrder returns the 1-based identity finish order [1,2,...,n], a
// valid permutation for exercising GenerateOdds in unit tests where the
// specific finish ordering does not matter.
func identityOrder(n int) []int {
	o := make([]int, n)
	for i := range o {
		o[i] = i + 1
	}
	return o
}

func TestOddsLength_dog8(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	got := GenerateOdds(mustMT(t), cfg, identityOrder(cfg.WinOddsCount))
	if len(got) != 64 {
		t.Fatalf("len(odds)=%d, want 64", len(got))
	}
}

func TestOddsLength_dog6(t *testing.T) {
	cfg := mustConfig(t, "dog6")
	got := GenerateOdds(mustMT(t), cfg, identityOrder(cfg.WinOddsCount))
	if len(got) != 36 {
		t.Fatalf("len(odds)=%d, want 36", len(got))
	}
}

// oddsOverroundProbe validates the PER-RACE overround model (drawOverroundTarget,
// DS-fitted lognormal): each race's overround must land inside the configured
// clamp band [OverroundShift, OverroundTarget+0.08], and the MEDIAN over many
// races must sit on OverroundTarget (the lognormal median is the target by
// construction). It deliberately does NOT assert a tight ±tolerance per race —
// that was the old fixed-target behavior; the vendor varies its margin
// race-to-race and we now reproduce that spread (see odds.go, doc 08 §4.6).
func oddsOverroundProbe(t *testing.T, gameType string) {
	t.Helper()
	cfg := mustConfig(t, gameType)
	const trials = 2000
	lo := cfg.OverroundShift
	hi := cfg.OverroundTarget + 0.08
	ovs := make([]float64, 0, trials)
	outOfBand := 0
	for i := 0; i < trials; i++ {
		seed := fmt.Sprintf("%064x", uint64(i+1)*0x9e3779b97f4a7c15)
		mt, err := rng.NewMT19937WithSeedHex(seed)
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		got := GenerateOdds(mt, cfg, identityOrder(cfg.WinOddsCount))
		var overround float64
		for j := 0; j < cfg.WinOddsCount; j++ {
			overround += 1.0 / got[j]
		}
		if overround < lo-cfg.OverroundTolerance || overround > hi+cfg.OverroundTolerance {
			outOfBand++
		}
		ovs = append(ovs, overround)
	}
	if outOfBand > 0 {
		t.Errorf("%s: %d/%d races outside overround band [%.3f, %.3f]", gameType, outOfBand, trials, lo, hi)
	}
	sort.Float64s(ovs)
	median := ovs[len(ovs)/2]
	if d := math.Abs(median - cfg.OverroundTarget); d > 0.004 {
		t.Errorf("%s: median overround %.4f vs target %.4f, Δ=%.4f > 0.004", gameType, median, cfg.OverroundTarget, d)
	}
}

func TestOddsOverroundPerRaceModel_dog8(t *testing.T) { oddsOverroundProbe(t, "dog8") }
func TestOddsOverroundPerRaceModel_dog6(t *testing.T) { oddsOverroundProbe(t, "dog6") }

func TestWinOddsClampedToConstraints(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	// Post-shuffle the per-position constraint is lost; verify the global
	// envelope per the legacy fallback bounds.
	got := GenerateOdds(mustMT(t), cfg, identityOrder(cfg.WinOddsCount))
	for j := 0; j < cfg.WinOddsCount; j++ {
		if got[j] < 2.0 || got[j] > cfg.MaxOdds {
			t.Errorf("WIN odd[%d]=%v outside [2.0, %v]", j, got[j], cfg.MaxOdds)
		}
	}
}

// TestForecastOddsBounded exercises both lower and upper envelopes plus a
// NaN/Inf trap. The lower bound (> 0) by itself is tautological — WIN ≥
// 2.5 fallback floor × WIN ≥ 2.5 × CombinedFactor.Min > 0 — so it can
// never fail. The upper bound catches a future overflow if WIN ever
// produces a very large value × a large CombinedFactor draw.
func TestForecastOddsBounded(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	got := GenerateOdds(mustMT(t), cfg, identityOrder(cfg.WinOddsCount))
	// Theoretical max forecast = MaxOdds^2 * CombinedFactor.Max plus
	// rounding slack. Use 2× that as a generous trap.
	upper := cfg.MaxOdds * cfg.MaxOdds * cfg.CombinedFactor.Max * 2
	for j := cfg.WinOddsCount; j < len(got); j++ {
		v := got[j]
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("FORECAST odd[%d] is NaN/Inf: %v", j, v)
		}
		if v <= 0 {
			t.Errorf("FORECAST odd[%d]=%v, want > 0", j, v)
		}
		if v > upper {
			t.Errorf("FORECAST odd[%d]=%v exceeds upper trap %v — investigate overflow/calibration drift", j, v, upper)
		}
	}
}

func TestOddsDeterministic(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	a := GenerateOdds(mustMT(t), cfg, identityOrder(cfg.WinOddsCount))
	b := GenerateOdds(mustMT(t), cfg, identityOrder(cfg.WinOddsCount))
	if len(a) != len(b) {
		t.Fatalf("len mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("odds[%d]: a=%v b=%v", i, a[i], b[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Odds↔finish coupling tests
// ---------------------------------------------------------------------------

// withTheta returns a copy of cfg with the coupling dispersion overridden.
// withTheta forces the Mallows coupling path at the given theta, disabling the
// Plackett-Luce path so the theta-specific invariants below (theta=0 ⇒ flat,
// theta>0 ⇒ favorite-bias, value-multiset preservation) test the Mallows
// FALLBACK in isolation. The live PL path is covered by TestPLLiveMarginal.
func withTheta(cfg config.GameTypeConfigExt, theta float64) config.GameTypeConfigExt {
	cfg.OddsFinishCoupling.Theta = theta
	cfg.OddsFinishCoupling.UsePL = false
	cfg.OddsFinishCoupling.Weights = nil
	return cfg
}

func sortedWinMultiset(odds []float64, n int) []float64 {
	cp := make([]float64, n)
	copy(cp, odds[:n])
	sort.Float64s(cp)
	return cp
}

// TestCouplingPreservesWinMultiset proves the coupling only changes
// ASSIGNMENT, never the per-round VALUE multiset: for a fixed seed the
// sorted WIN-odds multiset is identical at theta=0 and theta>0. mallowsAssign
// consumes exactly n CertifiedFloat draws regardless of theta, so the
// value-generation stream (which runs before assignment) is unaffected.
func TestCouplingPreservesWinMultiset(t *testing.T) {
	for _, gt := range []string{"dog8", "dog6"} {
		base := mustConfig(t, gt)
		n := base.WinOddsCount
		order := identityOrder(n)
		a := GenerateOdds(mustMT(t), withTheta(base, 0), order)
		b := GenerateOdds(mustMT(t), withTheta(base, 0.5), order)
		ma := sortedWinMultiset(a, n)
		mb := sortedWinMultiset(b, n)
		for i := range ma {
			if ma[i] != mb[i] {
				t.Fatalf("%s: WIN multiset changed by theta: theta0[%d]=%v thetaX=%v", gt, i, ma[i], mb[i])
			}
		}
	}
}

// TestCouplingOverroundInvariant proves the overround (hence RTP) of the
// WIN block is unchanged by coupling — it is a function only of the value
// multiset, which coupling preserves.
func TestCouplingOverroundInvariant(t *testing.T) {
	for _, gt := range []string{"dog8", "dog6"} {
		base := mustConfig(t, gt)
		n := base.WinOddsCount
		order := identityOrder(n)
		a := GenerateOdds(mustMT(t), withTheta(base, 0), order)
		b := GenerateOdds(mustMT(t), withTheta(base, 1.0), order)
		var ora, orb float64
		for j := 0; j < n; j++ {
			ora += 1.0 / a[j]
			orb += 1.0 / b[j]
		}
		if math.Abs(ora-orb) > 1e-9 {
			t.Fatalf("%s: overround changed by coupling: %v vs %v", gt, ora, orb)
		}
	}
}

// TestMallowsAssignUniformAtThetaZero checks pi is a valid permutation and,
// at theta=0, P(pi[0]=v) is flat (~1/n) — the legacy uncorrelated behavior.
func TestMallowsAssignDistribution(t *testing.T) {
	mt := mustMT(t)
	n := 8
	const draws = 200000

	// theta = 0 → uniform: P(pi[0]=v) ≈ 1/n for all v.
	cnt := make([]int, n)
	for d := 0; d < draws; d++ {
		pi := mallowsAssign(mt, n, 0)
		assertPermutation(t, pi, n)
		cnt[pi[0]]++
	}
	exp := float64(draws) / float64(n)
	for v := 0; v < n; v++ {
		dev := math.Abs(float64(cnt[v])-exp) / exp
		if dev > 0.05 {
			t.Errorf("theta=0: pi[0]=%d freq %.1f%% deviates >5%% from uniform", v, 100*float64(cnt[v])/draws)
		}
	}

	// Large theta → near-identity: pi[0]=0 almost always.
	mt2 := mustMT(t)
	id0 := 0
	for d := 0; d < draws; d++ {
		pi := mallowsAssign(mt2, n, 8.0)
		if pi[0] == 0 {
			id0++
		}
	}
	if frac := float64(id0) / draws; frac < 0.95 {
		t.Errorf("theta=8: P(pi[0]=0)=%.3f, want ≥0.95 (near-identity)", frac)
	}
}

func assertPermutation(t *testing.T, pi []int, n int) {
	t.Helper()
	if len(pi) != n {
		t.Fatalf("len(pi)=%d, want %d", len(pi), n)
	}
	seen := make([]bool, n)
	for _, v := range pi {
		if v < 0 || v >= n || seen[v] {
			t.Fatalf("pi is not a permutation of 0..%d: %v", n-1, pi)
		}
		seen[v] = true
	}
}

// genWinByFinish runs many isolated WIN-odds draws with a *random* finish
// order each round and returns, per finish-rank, the mean winning-runner odds
// and the per-physical-index mean odds, plus P(win|odds-rank).
func genWinByFinish(t *testing.T, gt string, theta float64, rounds int) (winnerMean, fieldMean float64, perIndexMean []float64, pWinByRank []float64) {
	t.Helper()
	base := mustConfig(t, gt)
	cfg := withTheta(base, theta)
	n := cfg.WinOddsCount
	mt := mustMT(t)

	perIndexSum := make([]float64, n)
	winsByRank := make([]int, n)
	var wSum, fSum float64
	var fCnt int

	for r := 0; r < rounds; r++ {
		// Random finish order (permutation of 1..n) drawn from the same
		// certified stream — mirrors the video selector's uniform-by-slot
		// winner.
		order := identityOrder(n)
		rng.CertifiedShuffle(mt, order)

		odds := GenerateOdds(mt, cfg, order)
		win := odds[:n]

		winnerSlot := order[0] - 1 // finish-rank 0 = winner
		wSum += win[winnerSlot]
		for i := 0; i < n; i++ {
			perIndexSum[i] += win[i]
			fSum += win[i]
			fCnt++
		}
		winsByRank[oddsRankOf(win, winnerSlot)]++
	}

	perIndexMean = make([]float64, n)
	for i := range perIndexMean {
		perIndexMean[i] = perIndexSum[i] / float64(rounds)
	}
	pWinByRank = make([]float64, n)
	for r := range pWinByRank {
		pWinByRank[r] = 100 * float64(winsByRank[r]) / float64(rounds)
	}
	return wSum / float64(rounds), fSum / float64(fCnt), perIndexMean, pWinByRank
}

// oddsRankOf returns the 0-based ascending rank of slot idx within odds
// (0 = lowest odds = favorite); ties broken by lower index.
func oddsRankOf(odds []float64, idx int) int {
	rank := 0
	for i := range odds {
		if i == idx {
			continue
		}
		if odds[i] < odds[idx] || (odds[i] == odds[idx] && i < idx) {
			rank++
		}
	}
	return rank
}

// TestWinnerOddsBelowFieldWhenCoupled: with theta>0 the winner's mean odds
// must be below the field mean (favorites win more).
func TestWinnerOddsBelowFieldWhenCoupled(t *testing.T) {
	for _, gt := range []string{"dog8", "dog6"} {
		cfg := mustConfig(t, gt)
		wMean, fMean, _, _ := genWinByFinish(t, gt, cfg.OddsFinishCoupling.Theta, 40000)
		if wMean >= fMean {
			t.Errorf("%s: winner mean odds %.3f not below field mean %.3f", gt, wMean, fMean)
		}
	}
}

// TestPerIndexMeanFlat: per-physical-index mean odds stays flat (within
// tolerance) regardless of theta — because the winner is uniform-by-slot,
// coupling redistributes values across finish-ranks, not slots. This is the
// invariant that keeps parity_odds per-index marginals passing.
func TestPerIndexMeanFlat(t *testing.T) {
	for _, gt := range []string{"dog8", "dog6"} {
		cfg := mustConfig(t, gt)
		_, _, perIndex, _ := genWinByFinish(t, gt, cfg.OddsFinishCoupling.Theta, 60000)
		var lo, hi = perIndex[0], perIndex[0]
		for _, v := range perIndex {
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		if hi-lo > 0.20 {
			t.Errorf("%s: per-index mean spread %.3f too large (flat expected): %v", gt, hi-lo, perIndex)
		}
	}
}

// TestThetaZeroWinFlat: theta=0 ⇒ P(win|odds-rank) flat at ~1/n (legacy
// regression — uncorrelated).
func TestThetaZeroWinFlat(t *testing.T) {
	for _, gt := range []string{"dog8", "dog6"} {
		cfg := mustConfig(t, gt)
		n := cfg.WinOddsCount
		_, _, _, pWin := genWinByFinish(t, gt, 0, 80000)
		uniform := 100.0 / float64(n)
		for r, p := range pWin {
			if math.Abs(p-uniform) > 1.5 {
				t.Errorf("%s theta=0: P(win|rank %d)=%.2f%% deviates >1.5pp from uniform %.2f%%", gt, r+1, p, uniform)
			}
		}
	}
}

// TestPLLiveMarginal: the LIVE Plackett-Luce coupling reproduces its target
// marginal P(win|odds-rank) ≈ Weights/ΣWeights (the DS win marginal), and the
// winner mean odds stays below the field mean (favorites win more). This is the
// production-path counterpart to the Mallows-only TestThetaZeroWinFlat /
// TestWinnerOddsBelowFieldWhenCoupled (which run with PL disabled via withTheta).
func TestPLLiveMarginal(t *testing.T) {
	for _, gt := range []string{"dog8", "dog6"} {
		base := mustConfig(t, gt)
		if !base.OddsFinishCoupling.UsePL {
			t.Fatalf("%s: expected live config to use Plackett-Luce", gt)
		}
		n := base.WinOddsCount
		mt := mustMT(t)

		var wSum float64
		for _, w := range base.OddsFinishCoupling.Weights {
			wSum += w
		}

		winsByRank := make([]int, n)
		var winnerSum, fieldSum float64
		var fieldCnt int
		const rounds = 80000
		for r := 0; r < rounds; r++ {
			order := identityOrder(n)
			rng.CertifiedShuffle(mt, order)
			odds := GenerateOdds(mt, base, order)
			win := odds[:n]
			winnerSlot := order[0] - 1
			winnerSum += win[winnerSlot]
			for i := 0; i < n; i++ {
				fieldSum += win[i]
				fieldCnt++
			}
			winsByRank[oddsRankOf(win, winnerSlot)]++
		}

		for r := 0; r < n; r++ {
			got := 100 * float64(winsByRank[r]) / float64(rounds)
			want := 100 * base.OddsFinishCoupling.Weights[r] / wSum
			if math.Abs(got-want) > 1.5 {
				t.Errorf("%s PL: P(win|rank %d)=%.2f%% deviates >1.5pp from weight target %.2f%%", gt, r+1, got, want)
			}
		}
		if wMean, fMean := winnerSum/float64(rounds), fieldSum/float64(fieldCnt); wMean >= fMean {
			t.Errorf("%s PL: winner mean odds %.3f not below field mean %.3f", gt, wMean, fMean)
		}
	}
}

// TestFallbackPathCoupled: even when the normal 100-attempt loop never
// converges (forced by a degenerate config), the fallback odds are still
// coupled — winner mean below field mean at theta>0.
func TestFallbackPathCoupled(t *testing.T) {
	base := mustConfig(t, "dog8")
	// Force every convergence attempt to fail without collapsing the value
	// spread: keep the real overround target but set tolerance to 0. With
	// 1dp rounding the adjusted overround essentially never lands EXACTLY
	// on target, so all 100 attempts miss and generateWinOdds routes to the
	// fallback — whose varied weight-based odds still exercise coupling.
	base.OverroundTolerance = 0.0
	n := base.WinOddsCount
	mt := mustMT(t)

	var wSum, fSum float64
	var fCnt int
	const rounds = 40000
	for r := 0; r < rounds; r++ {
		order := identityOrder(n)
		rng.CertifiedShuffle(mt, order)
		odds := GenerateOdds(mt, withTheta(base, 0.6), order)
		win := odds[:n]
		wSum += win[order[0]-1]
		for i := 0; i < n; i++ {
			fSum += win[i]
			fCnt++
		}
	}
	wMean := wSum / float64(rounds)
	fMean := fSum / float64(fCnt)
	if wMean >= fMean {
		t.Errorf("fallback path not coupled: winner mean %.3f not below field mean %.3f", wMean, fMean)
	}
}
