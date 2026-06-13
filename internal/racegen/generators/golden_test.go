package generators

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
)

// TestGoldenRoundFromSeedHex pins the COMPLETE generated round (identity,
// outcome, odds, names, bonus, jackpot) produced from a fixed seed-hex
// (testSeedHex = 0x42*32) at a fixed slot (fixedSlot = 2026-05-17 12:00 UTC),
// for dog8 and dog6.
//
// This is the end-to-end reproducibility evidence GLI-19 expects: a frozen
// seed → frozen output mapping that exercises the full pipeline (video draw
// via the certified RNG, finish, odds↔finish coupling, competitor names with
// cooldown, bonus cascade, jackpot). Any algorithm or calibration change that
// alters output MUST fail this test loudly.
//
// If a change is INTENTIONAL (e.g. recalibrating Theta, retuning IPF), the
// values below must be rebaselined AND the certification evidence noted — a
// seed→output remap invalidates any previously submitted RNG/round fixtures.
// Odds are compared via their JSON encoding to avoid float-representation
// flakiness; jackpot via 2-decimal formatting (it is rounded to 2dp).
//
// REBASELINED 2026-05-31: the per-race overround draw (drawOverroundTarget in
// odds.go, DS-fitted lognormal) consumes one extra certified Normal per race,
// which re-maps odds + jackpot. Finish/first/second/video/bonus/names are
// UNCHANGED (the draw happens inside WIN-odds generation, after the finish
// draw) — the certifiable video-selection/winner surface is untouched.
//
// RE-BASELINED 2026-06-01 (dog8 odds): DS odds-matrix parity — (a) dog8
// CombinedFactor.Mean 0.992→1.009 (forecast was ~1.6% short of DS); (b) the
// per-race SHAPE concentration κ (drawShapeConcentration, split-lognormal) was
// added, consuming one extra certified Normal per dog8 race; (c) the favorite
// PositionConstraint was re-tuned to Mean 4.55 / Std 0.75 to reach the DS
// favorite extremes. The κ draw happens inside WIN-odds generation, AFTER the
// finish/video draw, so finish/first/second/video/bonus are UNCHANGED.
//
// RE-BASELINED 2026-06-01 (dog6 odds + jackpot): same κ shape-concentration
// model now enabled for dog6 (symmetric ShapeConcentrationSigma 0.24, no σ_lo —
// DS dog6 favorite tails are near-symmetric) plus widened favorite/longshot
// clamps to let the κ-driven tails reach the DS extremes. dog6's κ draw is now
// active, so it consumes one extra certified Normal per race; because the
// jackpot draw follows odds generation in the stream, dog6 odds AND jackpot
// move. finish/first/second/video/bonus stay UNCHANGED (the κ draw is after the
// finish/video draw — the test confirms it). The certifiable video-selection
// /winner surface is untouched for both game types.
//
// RE-BASELINED 2026-06-02 (dog8+dog6 odds + jackpot): the rank-space gap model
// (RankGapModel, config.RankGap) now generates WIN odds as already-sorted
// adjacent gaps to close the within-matrix odds-repeat-rate gap (P(matrix has
// tie): dog8 25.7% vs DS 26.7%, dog6 17.7% vs DS 18.6%, both ≤1pp). The gap
// draws consume a different count of certified Normals per race than the prior
// per-position draws, re-mapping odds AND the downstream jackpot for both game
// types. finish/first/second/video/bonus/names stay UNCHANGED (the rank-space
// draws happen inside WIN-odds generation, after the finish/video draw — the
// test confirms it). The certifiable video-selection/winner surface is
// untouched for both game types.
//
// RE-BASELINED 2026-06-02 (dog8+dog6 odds + jackpot): the odds↔finish coupling
// switched from the single-theta Mallows RIM to Plackett-Luce
// (OddsFinishCoupling.UsePL, Weights = DS win marginal) to fix the near-flat
// conditional P(2nd-rank|1st-rank) that Mallows produced — the lever behind the
// residual exacta-cell +EV (test/parity_exacta). The PL draw is a permutation
// of the SAME per-round odds-VALUE multiset (overround/RTP and per-index
// marginals unchanged) but assigns values to slots differently, and consumes a
// different count of certified Floats than Mallows, re-mapping the WIN+forecast
// odds AND the downstream jackpot. finish/first/second/video/bonus/names stay
// UNCHANGED (the coupling only re-labels odds values onto the already-chosen
// finish order — the test confirms first=2/second=8(dog8)/6(dog6) and
// video=R0291/R0489 are intact). The certifiable video-selection/winner surface
// is untouched.
//
// RE-BASELINED 2026-06-02 (dog8+dog6 FORECAST odds + jackpot): the forecast
// combined factor is now rank-aware (ForecastRankFactor, config.ForecastRank) —
// favorite-led exacta pairs priced shorter, scaling up to longshot-led, to
// match the DS rank-aware exacta pricing and close the favorite-first exacta
// +EV. WIN odds (first n entries) are UNCHANGED; only the FORECAST tail moves.
// The per-pair factor still draws exactly one certified Normal, but its
// rank-dependent mean changes the rejection-sampling consumption, re-mapping the
// downstream jackpot. finish/first/second/video/bonus/names stay UNCHANGED (the
// forecast draw is after the finish/video draw). The certifiable
// video-selection/winner surface is untouched.
//
// RE-BASELINED 2026-06-12 (FULL remap — finish/video/names/odds/jackpot):
// CertifiedFloat moved from 32-bit to 53-bit resolution (two uint32 draws
// per float — finding H6 of docs/AUDITORIA-RNG-GLI19.md; eliminates the
// 2^-32 quantization of IPF/Plackett-Luce probabilities). Unlike prior
// rebaselines this changes EVERY float draw including the video selection,
// so finish/first/second/video move too. The HMAC-DRBG source swap itself
// does NOT alter this test (it pins the MT19937 lab path). ONE candidate
// remap remains open before code freeze: plan D4 (inverse-CDF truncated
// normal replacing Box-Muller+clamp, docs/PLAN-CERTIFICACION-GLI19.md) —
// if adopted it MUST land before any Phase-5 evidence collection, since it
// changes per-draw stream consumption and would force both a rebaseline
// here and regeneration of collected evidence. Decide D4 first; collect
// evidence second.
func TestGoldenRoundFromSeedHex(t *testing.T) {
	cases := []struct {
		gt        string
		roundCode string
		idRace    int
		first     int
		second    int
		videoID   string
		bonus     int
		jackpot   string // "%.2f"
		names     []string
		oddsJSON  string
	}{
		{
			gt:        "dog8",
			roundCode: "GA541_105_202605170210",
			idRace:    210,
			first:     3,
			second:    6,
			videoID:   "R0172",
			bonus:     1,
			jackpot:   "45005.11",
			names:     []string{"Robbin", "Gonzo", "Utah", "Trent", "Clue", "Juwel", "Destiny", "Velvet"},
			oddsJSON:  `[10.8,5.1,3.6,6.3,14.7,6.4,7.5,16.5,60.7,38.3,66.4,168.7,70.8,85,182.4,57.3,17.6,29.6,75.2,33.1,34.6,71.7,34.6,15.8,20.9,49,20,23.4,53.4,66.6,31.1,22.3,97.3,37.4,43,99.4,169.9,79.6,51.2,99.1,96.5,111.2,260.8,73.5,31.4,24.9,42.6,94.9,50,102.1,79.9,40.7,28.2,50.8,113.2,54.5,127.3,181.2,92.5,65.6,115.8,259.5,116.6,134.6]`,
		},
		{
			gt:        "dog6",
			roundCode: "GA141_101_202605170211",
			idRace:    211,
			first:     6,
			second:    4,
			videoID:   "R0222",
			bonus:     1,
			jackpot:   "45001.73",
			names:     []string{"Robbin", "Gonzo", "Utah", "Trent", "Clue", "Juwel"},
			oddsJSON:  `[6.9,7.9,4.3,5.8,3.7,4.7,54.2,26.9,40,26.7,32.1,52.8,36.3,42.7,30.7,39.8,26.1,32.5,20.8,14.8,19.8,41.7,46.6,24.3,19.9,26.8,25.1,23.6,14.4,18.7,14,30.8,36.9,18.6,27.6,16.8]`,
		},
	}

	for _, c := range cases {
		t.Run(c.gt, func(t *testing.T) {
			cfg := mustConfig(t, c.gt)
			sel := realSelector(t, c.gt)
			// jackpot pins below assume freshState() starts at 45000.0.
			g, err := GenerateGame(mustMT(t), cfg, sel, freshState(), fixedSlot(), nil, nil)
			if err != nil {
				t.Fatalf("GenerateGame: %v", err)
			}

			if g.RoundCode != c.roundCode {
				t.Errorf("RoundCode = %q, want %q", g.RoundCode, c.roundCode)
			}
			if g.IDRace != c.idRace {
				t.Errorf("IDRace = %d, want %d", g.IDRace, c.idRace)
			}
			if g.Finish.First != c.first {
				t.Errorf("Finish.First = %d, want %d", g.Finish.First, c.first)
			}
			if g.Finish.Second != c.second {
				t.Errorf("Finish.Second = %d, want %d", g.Finish.Second, c.second)
			}
			if vid := ExtractVideoID(g.Finish.VideoName.MP4); vid != c.videoID {
				t.Errorf("videoID = %q, want %q (outcome draw changed)", vid, c.videoID)
			}
			if g.Bonus != c.bonus {
				t.Errorf("Bonus = %d, want %d", g.Bonus, c.bonus)
			}
			if jp := fmt.Sprintf("%.2f", g.JackpotInfo.BonusValue); jp != c.jackpot {
				t.Errorf("jackpot = %s, want %s", jp, c.jackpot)
			}

			gotNames := make([]string, cfg.NumberCompetitor)
			for i := 1; i <= cfg.NumberCompetitor; i++ {
				gotNames[i-1] = g.Competitors[strconv.Itoa(i)].Name
			}
			gotNamesJSON, _ := json.Marshal(gotNames)
			wantNamesJSON, _ := json.Marshal(c.names)
			if string(gotNamesJSON) != string(wantNamesJSON) {
				t.Errorf("names = %s, want %s", gotNamesJSON, wantNamesJSON)
			}

			gotOddsJSON, _ := json.Marshal(g.Odds)
			if string(gotOddsJSON) != c.oddsJSON {
				t.Errorf("odds = %s\nwant   %s", gotOddsJSON, c.oddsJSON)
			}
		})
	}
}
