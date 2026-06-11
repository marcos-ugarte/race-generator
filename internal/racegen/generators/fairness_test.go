package generators

import (
	"sort"
	"testing"
	"time"

	"vg-racegen/internal/racegen/config"
	"vg-racegen/internal/racegen/data"
	"vg-racegen/internal/racegen/rng"
	"vg-racegen/internal/racegen/videoselector"
)

// TestWinMarketFairness is the GLI-19 §4.6.2 / §4.7 fairness evidence for the
// WIN market: it measures, over a large deterministic sample, the relationship
// between the DISPLAYED win-odds and the TRUE win probability the generator
// produces, plus the return-to-player (RTP) implied by that relationship.
//
// Why this matters for certification (distinct from the RNG chapter):
//   - §4.7 requires a minimum RTP (we use the conservative 75% floor). For a
//     parimutuel-style fixed-odds WIN bet, the realised RTP of betting a runner
//     is P(that runner wins) × its decimal odds.
//   - §4.6.2 requires that displayed odds reflect the true probability of the
//     event "unless otherwise disclosed". We verify the displayed odds are an
//     honest representation of P(win) (no odds-rank is a materially positive-EV
//     bet, i.e. RTP ≤ 1.0) and that empirical P(win|odds-rank) tracks the
//     de-overround implied probability of that rank.
//
// NOTE on the calibration check: because the Mallows coupling assigns the
// ascending-sorted odds multiset to finish ranks, lower odds ⇔ better finish
// rank by construction, so P(win|rank) ≈ implied prob is partly a CONSISTENCY
// check (it confirms the coupling and overround weren't broken), not fully
// independent corroboration. The RTP bounds are the load-bearing fairness
// gate. The tolerances below are deliberately loose FLOORS (the 3.5pp band is
// ~30× the Monte-Carlo SE at this N): they catch a gross calibration break,
// not a few-pp drift — tighten only if a regulator asks.
//
// The generator draws the winner from the IPF-fitted video pool (near-uniform
// by slot) and then assigns the odds VALUE multiset to slots via the Mallows
// coupling (config OddsFinishCoupling.Theta), so favourites tend to win. This
// test confirms that emergent behaviour is FAIR, not just vendor-matching
// (that is what test/parity_joint checks). Run deterministically from a fixed
// seed with NO ModifyStateBetweenGames so the evidence is reproducible.
//
// If a calibration change (Theta, overround, position constraints, IPF
// targets) breaks a gate here, that is a genuine fairness regression — fix the
// calibration, do not loosen the gate.
func TestWinMarketFairness(t *testing.T) {
	if testing.Short() {
		t.Skip("fairness Monte-Carlo skipped in -short")
	}

	const (
		sampleN = 150_000
		seed    = 42

		rtpFloor = 0.75 // GLI §4.7 conservative minimum RTP
		rtpCap   = 1.00 // §4.6.2: no odds-rank may be a positive-EV bet
		// calibration tolerance: |empirical P(win|rank) − de-overround implied
		// prob of that rank|, in probability points. Generous vs the ~1.2pp
		// worst case observed so MC noise / minor recalibration won't flake.
		calibTolPP = 0.035
		// overround sanity band around the configured target.
		overroundTol = 0.01
	)

	for _, gt := range []string{"dog8", "dog6"} {
		t.Run(gt, func(t *testing.T) {
			cfg := mustConfig(t, gt)
			fr := measureFairness(t, cfg, sampleN, seed)
			nr := cfg.NumberCompetitor

			t.Logf("=== %s N=%d meanOverround=%.4f (target %.4f) ===",
				gt, sampleN, fr.meanOverround, cfg.OverroundTarget)
			t.Logf("rank | P(win)%% | meanOdds | impliedDeOR%% | RTP")
			for r := 0; r < nr; r++ {
				t.Logf("  %d  | %6.2f | %7.3f | %9.2f | %.4f",
					r+1, fr.pWin[r]*100, fr.meanOdds[r], fr.impliedDeOR[r]*100, fr.rtp[r])
			}
			t.Logf("RTP always-favorite=%.4f  uniform-random=%.4f  mean-winner-odds=%.3f",
				fr.rtp[0], fr.rtpUniform, fr.meanWinnerOdds)

			// Overround sanity.
			if d := abs(fr.meanOverround - cfg.OverroundTarget); d > overroundTol {
				t.Errorf("meanOverround=%.4f deviates from target %.4f by %.4f (> %.4f)",
					fr.meanOverround, cfg.OverroundTarget, d, overroundTol)
			}

			// Favourite bias: P(win|rank) non-increasing, favourite clearly
			// ahead of the longshot.
			for r := 0; r+1 < nr; r++ {
				if fr.winsByRank[r] < fr.winsByRank[r+1] {
					t.Errorf("P(win) not monotone: rank %d (%d wins) < rank %d (%d wins)",
						r+1, fr.winsByRank[r], r+2, fr.winsByRank[r+1])
				}
			}
			if fr.pWin[0] < 1.8*fr.pWin[nr-1] {
				t.Errorf("favourite bias too weak: P(win|fav)=%.4f, P(win|long)=%.4f (want fav ≥ 1.8×)",
					fr.pWin[0], fr.pWin[nr-1])
			}

			// §4.7 / §4.6.2: every odds-rank RTP within [floor, cap].
			for r := 0; r < nr; r++ {
				if fr.rtp[r] < rtpFloor {
					t.Errorf("rank %d RTP=%.4f below GLI floor %.2f", r+1, fr.rtp[r], rtpFloor)
				}
				if fr.rtp[r] > rtpCap {
					t.Errorf("rank %d RTP=%.4f exceeds %.2f — positive-EV bet (odds understate P(win))",
						r+1, fr.rtp[r], rtpCap)
				}
			}

			// §4.6.2 calibration: empirical P(win|rank) ≈ de-overround implied
			// probability of that rank.
			for r := 0; r < nr; r++ {
				if d := abs(fr.pWin[r] - fr.impliedDeOR[r]); d > calibTolPP {
					t.Errorf("rank %d miscalibrated: P(win)=%.4f vs impliedDeOR=%.4f, |Δ|=%.4f (> %.3f)",
						r+1, fr.pWin[r], fr.impliedDeOR[r], d, calibTolPP)
				}
			}

			// Whole-market strategy RTPs also bounded.
			if fr.rtpUniform < rtpFloor || fr.rtpUniform > rtpCap {
				t.Errorf("uniform-random RTP=%.4f outside [%.2f, %.2f]", fr.rtpUniform, rtpFloor, rtpCap)
			}
		})
	}
}

// fairnessResult holds the per-odds-rank aggregates (rank 0 = favourite).
type fairnessResult struct {
	winsByRank     []int
	pWin           []float64 // winsByRank / N
	meanOdds       []float64 // mean odds VALUE at each rank
	impliedDeOR    []float64 // (1/meanOdds)/meanOverround
	rtp            []float64 // pWin * meanOdds
	meanOverround  float64
	meanWinnerOdds float64
	rtpUniform     float64 // mean over ranks of rtp (bet a uniform-random runner)
}

// measureFairness runs sampleN deterministic rounds (fixed seed, no
// state-modifier) and folds them into per-odds-rank fairness aggregates.
func measureFairness(t *testing.T, cfg config.GameTypeConfigExt, sampleN int, seed uint32) fairnessResult {
	t.Helper()
	pool := data.VideoPool(cfg.VideoPoolPath)
	if pool == nil {
		t.Fatalf("nil video pool for %q", cfg.VideoPoolPath)
	}
	sel, err := videoselector.New(pool, cfg)
	if err != nil {
		t.Fatalf("videoselector.New: %v", err)
	}
	nr := cfg.NumberCompetitor
	mt := rng.NewMT19937WithUint32Seed(seed)
	jp := &JackpotState{Current: JackpotInitialValue}
	recent := map[string]bool{}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	interval := time.Duration(cfg.RoundIntervalSec) * time.Second

	winsByRank := make([]int, nr)
	oddsSumByRank := make([]float64, nr)    // Σ odds value at rank r (reporting only)
	invOddsSumByRank := make([]float64, nr) // Σ (1/odds) at rank r — unbiased implied prob
	payoutByRank := make([]float64, nr)     // Σ winner payout when winner is at rank r
	var overroundSum, winnerOddsSum float64

	idx := make([]int, nr)
	for i := 0; i < sampleN; i++ {
		g, err := GenerateGame(mt, cfg, sel, jp, base.Add(time.Duration(i)*interval), recent, nil)
		if err != nil {
			t.Fatalf("GenerateGame: %v", err)
		}
		win := g.Odds[:nr]
		for k := range idx {
			idx[k] = k
		}
		sort.SliceStable(idx, func(a, b int) bool { return win[idx[a]] < win[idx[b]] })

		var orr float64
		winRank := 0
		for r, slot := range idx {
			oddsSumByRank[r] += win[slot]
			inv := 1.0 / win[slot]
			invOddsSumByRank[r] += inv
			orr += inv
			if slot == g.Finish.First-1 {
				winRank = r
			}
		}
		overroundSum += orr
		winsByRank[winRank]++
		// Realised payout of a 1-unit bet on the rank-winRank runner this
		// round (it won, so payout = its odds). Summing this and dividing by
		// N gives the EXACT RTP of "always bet odds-rank r" — no
		// product-of-means approximation, so within-rank Cov(odds, win) is
		// captured.
		payoutByRank[winRank] += win[g.Finish.First-1]
		winnerOddsSum += win[g.Finish.First-1]

		for _, c := range g.Competitors {
			recent[c.Name] = true
		}
	}

	fr := fairnessResult{
		winsByRank:     winsByRank,
		pWin:           make([]float64, nr),
		meanOdds:       make([]float64, nr),
		impliedDeOR:    make([]float64, nr),
		rtp:            make([]float64, nr),
		meanOverround:  overroundSum / float64(sampleN),
		meanWinnerOdds: winnerOddsSum / float64(sampleN),
	}
	for r := 0; r < nr; r++ {
		fr.pWin[r] = float64(winsByRank[r]) / float64(sampleN)
		fr.meanOdds[r] = oddsSumByRank[r] / float64(sampleN)
		// Mean per-round implied prob at rank r, de-overrounded. Uses
		// mean(1/odds), NOT 1/mean(odds) (the latter is Jensen-biased low at
		// high-variance ranks).
		meanInvOdds := invOddsSumByRank[r] / float64(sampleN)
		fr.impliedDeOR[r] = meanInvOdds / fr.meanOverround
		// EXACT realised RTP of always betting odds-rank r.
		fr.rtp[r] = payoutByRank[r] / float64(sampleN)
		fr.rtpUniform += fr.rtp[r] / float64(nr)
	}
	return fr
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
