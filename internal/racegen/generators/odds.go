package generators

import (
	"math"
	"sort"

	"vg-racegen/internal/racegen/config"
	"vg-racegen/internal/racegen/rng"
)

// GenerateOdds returns the full odds vector for one race: WIN odds first
// (length == cfg.WinOddsCount), then FORECAST odds in ordered-pair layout
// (i, j) with i != j; length == cfg.NumberOdds total.
//
// Algorithm follows the legacy odds.ts:29-105:
//
//  1. WIN: up to 100 attempts. Each attempt draws Normal-clamped raw odds
//     per position, scales by the overround-ratio factor, clamps per
//     position, rounds to 1dp. Accept if final overround is within
//     tolerance, then ASSIGN the odds VALUES to physical slots via the
//     odds↔finish coupling (so the per-position constraints aren't leaked
//     downstream AND favorites tend to win — see coupleWinOdds).
//  2. If 100 attempts fail to converge, fall back to a uniform-weight
//     procedure (odds.ts:68-83). The fallback runs through the SAME
//     coupling step — it must not silently produce uncorrelated odds.
//  3. FORECAST: for each ordered pair (i, j) with i != j, multiply
//     winOdds[i]*winOdds[j] by a Normal-clamped CombinedFactor and round
//     to 1dp. Runs on the final assigned WIN slice.
//
// finishOrder is the 1-based finish order (finishOrder[0] is the
// first-place runner). The coupling maps finish-rank → odds-value-rank so
// earlier finishers tend to get lower odds. With Theta=0 the assignment is
// uniform and the result is statistically identical to the legacy shuffle.
//
// dog63 extended-odds bet types (3-14) are not in scope for Phase 3 —
// only dog8 (NumberOdds=64) and dog6 (NumberOdds=36) are supported here.
func GenerateOdds(mt *rng.MT19937, cfg config.GameTypeConfigExt, finishOrder []int) []float64 {
	win := generateWinOdds(mt, cfg, finishOrder)
	forecast := generateForecastOdds(mt, win, cfg)

	out := make([]float64, 0, cfg.NumberOdds)
	out = append(out, win...)
	out = append(out, forecast...)
	return out
}

// generateWinOdds returns the coupled, in-tolerance WIN odds slice, or
// the (also-coupled) fallback if 100 attempts can't hit the tolerance.
//
// The per-race overround target is DRAWN from the DS-fitted shifted-lognormal
// (drawOverroundTarget) BEFORE the attempt loop, so the realized margin varies
// race-to-race exactly as the vendor's does, instead of every race collapsing
// to a single fixed target. The draw consumes one certified Normal per race.
func generateWinOdds(mt *rng.MT19937, cfg config.GameTypeConfigExt, finishOrder []int) []float64 {
	const maxAttempts = 100
	n := cfg.WinOddsCount

	ovTarget := drawOverroundTarget(mt, cfg)
	kappa := drawShapeConcentration(mt, cfg)

	if cfg.RankGap.Enabled {
		if out := generateWinOddsRankSpace(mt, cfg, finishOrder, ovTarget, kappa, maxAttempts); out != nil {
			return out
		}
		return generateWinOddsFallback(mt, cfg, finishOrder)
	}

	raw := make([]float64, n)
	adjusted := make([]float64, n)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		for i := 0; i < n; i++ {
			c := cfg.PositionConstraints[i]
			raw[i] = rng.CertifiedNormalClamped(mt, c.Mean, c.Std, c.Min, c.Max)
		}
		applyShapeConcentration(raw, kappa)
		factor := calcOverround(raw) / ovTarget

		for i := 0; i < n; i++ {
			c := cfg.PositionConstraints[i]
			v := raw[i] * factor
			if v < c.Min {
				v = c.Min
			} else if v > c.Max {
				v = c.Max
			}
			adjusted[i] = roundN(v, 1)
		}

		if diff := math.Abs(calcOverround(adjusted) - ovTarget); diff <= cfg.OverroundTolerance {
			return coupleWinOdds(mt, cfg, finishOrder, adjusted)
		}
	}

	return generateWinOddsFallback(mt, cfg, finishOrder)
}

// generateWinOddsRankSpace builds the WIN-odds vector ALREADY SORTED
// (ascending) by sampling adjacent gaps, per the RankGapModel (doc 09 §4.2).
// It returns the coupled slice on the first in-tolerance attempt, or nil if
// none of maxAttempts converges (caller falls back).
//
// This is the gated alternative to the per-position draw in generateWinOdds:
// it gives direct control over the favorite↔runner-up gap (hence the
// within-matrix 1dp tie rate). Everything downstream — shape concentration,
// the multiplicative overround rescale (which preserves order AND relative
// gaps), and the odds↔finish coupling — is identical to the HEAD path.
//
// Each attempt issues n certified-Normal CALLS (1 anchor + n-1 gaps), same as
// the per-position path. The number of underlying MT draws still differs,
// though: CertifiedNormalClamped rejection-samples, and the gap distributions
// reject at different rates than the per-position ones — so this path remaps
// the MT stream (and thus the downstream jackpot). That remap is intentional
// and is why golden_test.go is rebaselined.
func generateWinOddsRankSpace(mt *rng.MT19937, cfg config.GameTypeConfigExt, finishOrder []int, ovTarget, kappa float64, maxAttempts int) []float64 {
	n := cfg.WinOddsCount
	rg := cfg.RankGap
	s := make([]float64, n)
	adjusted := make([]float64, n)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// Favorite anchor reuses the calibrated position-0 marginal.
		fav := cfg.PositionConstraints[0]
		s[0] = rng.CertifiedNormalClamped(mt, fav.Mean, fav.Std, fav.Min, fav.Max)
		for r := 1; r < n; r++ {
			gap := rng.CertifiedNormalClamped(mt, rg.GapMean[r-1], rg.GapStd[r-1], rg.GapMin[r-1], rg.GapMax[r-1])
			if gap < 0 {
				gap = 0
			}
			s[r] = s[r-1] + gap
		}

		applyShapeConcentration(s, kappa)
		factor := calcOverround(s) / ovTarget

		for i := 0; i < n; i++ {
			v := s[i] * factor
			if v < rg.OddsFloor {
				v = rg.OddsFloor
			}
			v = softCapOdds(v, rg.LongshotMaxOdds, cfg.MaxOdds)
			adjusted[i] = roundN(v, 1)
		}

		if diff := math.Abs(calcOverround(adjusted) - ovTarget); diff <= cfg.OverroundTolerance {
			return coupleWinOdds(mt, cfg, finishOrder, adjusted)
		}
	}

	return nil
}

// softCapOdds maps odds above the soft cap into [soft, hard) via a smooth
// saturating curve (soft + span·over/(over+span)), instead of letting the
// hard MaxOdds clamp collapse every large value to a single point. It is
// monotone increasing, so it preserves rank order while keeping neighbouring
// longshots distinct — which avoids the artificial over-tie of the tail. For
// v <= soft it is the identity. soft must be < hard.
func softCapOdds(v, soft, hard float64) float64 {
	if v <= soft {
		return v
	}
	over := v - soft
	span := hard - soft
	return soft + span*over/(over+span)
}

// drawOverroundTarget samples the per-race overround target from the DS-fitted
// shifted-lognormal: ov = shift + (median-shift)*exp(sigma*Z), Z ~ certified
// N(0,1) clamped to ±4. Median == cfg.OverroundTarget by construction; the
// lognormal gives the right-skew the vendor exhibits. With OverroundLogSigma
// == 0 (or no shift configured) it returns the fixed target — legacy behavior.
// The result is clamped to a sane band so an extreme Z can't produce a
// degenerate book.
func drawOverroundTarget(mt *rng.MT19937, cfg config.GameTypeConfigExt) float64 {
	if cfg.OverroundLogSigma <= 0 || cfg.OverroundShift <= 0 || cfg.OverroundShift >= cfg.OverroundTarget {
		return cfg.OverroundTarget
	}
	z := rng.CertifiedNormalClamped(mt, 0, 1, -4, 4)
	ov := cfg.OverroundShift + (cfg.OverroundTarget-cfg.OverroundShift)*math.Exp(cfg.OverroundLogSigma*z)
	lo := cfg.OverroundShift
	hi := cfg.OverroundTarget + 0.08
	if ov < lo {
		ov = lo
	} else if ov > hi {
		ov = hi
	}
	return ov
}

// drawShapeConcentration samples the per-race shape factor kappa = exp(sigma*Z),
// Z ~ certified N(0,1) clamped ±4. kappa>1 concentrates the field (strong
// favorite, longer longshots), kappa<1 flattens it. The sigma is split: Z>0
// uses ShapeConcentrationSigma (concentrate), Z<0 uses ShapeConcentrationSigmaLo
// (flatten) when set, else the same sigma — a split-lognormal that gives DS's
// heavier strong-favorite tail. sigma==0 ⇒ kappa=1 (legacy fixed shape). One
// certified Normal per race. See ShapeConcentrationSigma.
func drawShapeConcentration(mt *rng.MT19937, cfg config.GameTypeConfigExt) float64 {
	if cfg.ShapeConcentrationSigma <= 0 {
		return 1.0
	}
	z := rng.CertifiedNormalClamped(mt, 0, 1, -4, 4)
	sigma := cfg.ShapeConcentrationSigma
	if z < 0 && cfg.ShapeConcentrationSigmaLo > 0 {
		sigma = cfg.ShapeConcentrationSigmaLo
	}
	return math.Exp(sigma * z)
}

// applyShapeConcentration stretches (kappa>1) or compresses (kappa<1) the raw
// odds in log space around their geometric mean G: odds_i = G*(odds_i/G)^kappa.
// G is preserved, so the subsequent overround rescale changes only the level,
// not the kappa-driven shape. No-op when kappa==1.
func applyShapeConcentration(odds []float64, kappa float64) {
	if kappa == 1.0 {
		return
	}
	var logsum float64
	for _, o := range odds {
		logsum += math.Log(o)
	}
	g := math.Exp(logsum / float64(len(odds)))
	for i, o := range odds {
		odds[i] = g * math.Pow(o/g, kappa)
	}
}

// generateWinOddsFallback mirrors odds.ts:68-83 — uniform weights with
// a +0.1 floor, normalize against the overround target, invert, then
// clamp to [2.5, MaxOdds] and round 1dp. The resulting VALUE multiset is
// routed through the same coupleWinOdds assignment so the fallback path
// also produces favorite-biased odds (it must NOT silently emit
// uncorrelated odds).
func generateWinOddsFallback(mt *rng.MT19937, cfg config.GameTypeConfigExt, finishOrder []int) []float64 {
	n := cfg.WinOddsCount
	weights := make([]float64, n)
	var total float64
	for i := 0; i < n; i++ {
		weights[i] = rng.CertifiedFloat(mt) + 0.1
		total += weights[i]
	}

	odds := make([]float64, n)
	for i := 0; i < n; i++ {
		p := (weights[i] / total) * cfg.OverroundTarget
		o := 1.0 / p
		if o < 2.5 {
			o = 2.5
		} else if o > cfg.MaxOdds {
			o = cfg.MaxOdds
		}
		odds[i] = roundN(o, 1)
	}
	return coupleWinOdds(mt, cfg, finishOrder, odds)
}

// coupleWinOdds assigns the computed WIN-odds VALUE multiset to physical
// slots, coupled to the chosen finish order via the Mallows model.
//
// It sorts the value multiset ascending into `sorted` (value-rank 0 =
// lowest odds = favorite), draws a Mallows permutation pi (pi[r] is the
// value-rank handed to finish-rank r), and scatters so the runner who
// finishes at rank r gets the pi[r]-th lowest odds:
//
//	out[finishOrder[r]-1] = sorted[pi[r]]
//
// Because `out` is a pure permutation of `values`, the per-round VALUE
// multiset (hence overround / RTP and per-index marginals over many
// rounds) is preserved exactly. With Theta=0 the Mallows draw is uniform,
// so this is statistically identical to the legacy CertifiedShuffle.
//
// finishOrder must be a permutation of 1..n. If it is missing or
// malformed (defensive), we fall back to a plain certified shuffle of the
// values so determinism and the value multiset are still preserved.
func coupleWinOdds(mt *rng.MT19937, cfg config.GameTypeConfigExt, finishOrder []int, values []float64) []float64 {
	n := len(values)

	sorted := make([]float64, n)
	copy(sorted, values)
	sort.Stable(sort.Float64Slice(sorted))

	if len(finishOrder) != n {
		// Defensive: no usable finish order. Preserve the multiset with a
		// uniform certified shuffle (legacy behavior).
		out := make([]float64, n)
		copy(out, values)
		rng.CertifiedShuffle(mt, out)
		return out
	}

	var pi []int
	if cfg.OddsFinishCoupling.UsePL && len(cfg.OddsFinishCoupling.Weights) == n {
		pi = plackettLuceAssign(mt, cfg.OddsFinishCoupling.Weights)
	} else {
		pi = mallowsAssign(mt, n, cfg.OddsFinishCoupling.Theta)
	}

	out := make([]float64, n)
	for r := 0; r < n; r++ {
		slot := finishOrder[r] - 1
		if slot < 0 || slot >= n {
			// Malformed finish order — fall back to uniform shuffle.
			cp := make([]float64, n)
			copy(cp, values)
			rng.CertifiedShuffle(mt, cp)
			return cp
		}
		out[slot] = sorted[pi[r]]
	}
	return out
}

// mallowsAssign returns a length-n permutation pi drawn from the Mallows
// model centered on the identity, with dispersion theta, via the Repeated
// Insertion Model (RIM). pi[r] is the odds-value-rank assigned to
// finish-rank r.
//
// RIM builds the permutation by inserting elements 0,1,…,n-1 one at a time
// into a growing list. Element i is inserted at position j ∈ {0..i} with
// probability proportional to exp(-theta*(i-j)) — i.e. larger theta biases
// each element toward the end of the list, concentrating the draw near the
// identity. The element labels are the finish-ranks; the final position of
// element r in the list is pi[r].
//
//   - theta == 0 ⇒ every insertion position is equiprobable ⇒ pi is a
//     uniformly random permutation (legacy behavior; P(win|rank) flat).
//   - theta  → ∞ ⇒ every element inserts at the end ⇒ pi is the identity
//     (finish-rank r always gets value-rank r; rank-1 favorite always wins).
//
// Uses ONLY rng.CertifiedFloat for the GLI-19 certified stream.
func mallowsAssign(mt *rng.MT19937, n int, theta float64) []int {
	// list holds element labels (finish-ranks) in current insertion order.
	list := make([]int, 0, n)
	weights := make([]float64, 0, n+1)
	for i := 0; i < n; i++ {
		// Build insertion weights for positions 0..i.
		weights = weights[:0]
		var total float64
		for j := 0; j <= i; j++ {
			w := math.Exp(-theta * float64(i-j))
			weights = append(weights, w)
			total += w
		}
		// Choose position via CertifiedFloat over the cumulative weights.
		x := rng.CertifiedFloat(mt) * total
		pos := i // default to last position (covers FP rounding at the tail)
		var acc float64
		for j := 0; j <= i; j++ {
			acc += weights[j]
			if x < acc {
				pos = j
				break
			}
		}
		// Insert element i at pos.
		list = append(list, 0)
		copy(list[pos+1:], list[pos:])
		list[pos] = i
	}

	// pi[r] = final position (value-rank) of element r (finish-rank r).
	pi := make([]int, n)
	for position, label := range list {
		pi[label] = position
	}
	return pi
}

// plackettLuceAssign returns a length-n permutation pi drawn from a
// Plackett-Luce model with weight vector w (indexed by odds-VALUE-rank, 0 =
// favorite). pi[r] is the value-rank assigned to finish-rank r.
//
// PL is sequential sampling WITHOUT replacement: finish-rank 0 (the winner) is
// assigned value-rank a with probability w[a]/Σw; finish-rank 1 then draws from
// the remaining ranks with probability w[b]/(Σw−w[a]); and so on. This makes
// BOTH the marginal P(1st=a)=w[a]/Σw and the conditional P(2nd=b|1st=a)=
// w[b]/(Σw−w[a]) explicit — unlike the single-theta Mallows, whose conditional
// is near-flat. With equal weights PL reduces to a uniform random permutation.
//
// Draws exactly n-1 rng.CertifiedFloat values (the last position is forced).
// Uses ONLY the GLI-19 certified stream.
func plackettLuceAssign(mt *rng.MT19937, w []float64) []int {
	n := len(w)
	avail := make([]int, n) // remaining value-ranks
	for i := range avail {
		avail[i] = i
	}
	pi := make([]int, n)
	for r := 0; r < n-1; r++ {
		var total float64
		for _, vr := range avail {
			total += w[vr]
		}
		x := rng.CertifiedFloat(mt) * total
		idx := len(avail) - 1 // default to last (covers FP rounding at the tail)
		var acc float64
		for k, vr := range avail {
			acc += w[vr]
			if x < acc {
				idx = k
				break
			}
		}
		pi[r] = avail[idx]
		avail = append(avail[:idx], avail[idx+1:]...)
	}
	pi[n-1] = avail[0] // forced last position
	return pi
}

// generateForecastOdds: ordered pairs (i, j) i != j, base = win[i]*win[j]
// times a Normal-clamped CombinedFactor, rounded 1dp. Length = n*(n-1).
func generateForecastOdds(mt *rng.MT19937, win []float64, cfg config.GameTypeConfigExt) []float64 {
	n := cfg.WinOddsCount
	cf := cfg.CombinedFactor

	// Rank map: rankOf[slot] = position of win[slot] in the odds sorted
	// ascending (0=favorite). win[] is per-SLOT (already permuted by
	// coupleWinOdds), so the favorite is whichever slot holds the lowest odds.
	// Tie-break by slot index to match the DS measurement harness (win[i], i).
	fr := cfg.ForecastRank
	var rankOf []int
	if fr.Enabled {
		order := make([]int, n)
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool {
			if win[order[a]] != win[order[b]] {
				return win[order[a]] < win[order[b]]
			}
			return order[a] < order[b]
		})
		rankOf = make([]int, n)
		for r, s := range order {
			rankOf[s] = r
		}
	}

	out := make([]float64, 0, n*(n-1))
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			// Rank-aware mean keyed on the first runner; flat fallback when
			// disabled. Exactly one certified draw per pair either way.
			mean, std, lo, hi := cf.Mean, cf.Std, cf.Min, cf.Max
			if fr.Enabled {
				mean, std, lo, hi = fr.Mean[rankOf[i]], fr.Std, fr.Min, fr.Max
			}
			factor := rng.CertifiedNormalClamped(mt, mean, std, lo, hi)
			out = append(out, roundN(win[i]*win[j]*factor, 1))
		}
	}
	return out
}

func calcOverround(odds []float64) float64 {
	var s float64
	for _, o := range odds {
		s += 1.0 / o
	}
	return s
}

func roundN(v float64, decimals int) float64 {
	mul := math.Pow(10, float64(decimals))
	return math.Round(v*mul) / mul
}
