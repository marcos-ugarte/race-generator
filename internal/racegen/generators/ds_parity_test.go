package generators

import (
	"math"
	"testing"
)

// TestGeneratedMatchesDS runs the FULL generator pipeline (GenerateGame) over a
// large deterministic sample and checks that what we EMIT — first/second-place
// distribution by box and the WIN-odds overround — matches the real DS vendor
// reference pinned in docs/racegen-design/08-DS-REFERENCE-STATS.md. This is the
// "no nos desviamos de DS" gate, complementary to TestDSReferenceDrift in the
// config package: that one checks our static targets against DS; this one checks
// the actual generated output (post-IPF draw + odds↔finish coupling) against DS.
//
// Only dog8 and dog6 are exercised here. horse_classic IS registered and
// generated as of 2026-06-09, but its ODDS are SMOKE-LEVEL (uniform targets,
// RankGap/ForecastRank/PL disabled — see config.horseClassicConfig doc): there
// is no per-box / per-rank DS odds reference for betoffer 241 to assert against
// (doc 08 §3 lists it only as a mixed field). Its finish POOL is real DS data,
// but the odds path is a placeholder, so it is deliberately excluded from the
// DS-stat parity battery until calibrated. dog63/horse follow later.
//
// Reusing one MT across N games advances the certified stream so each round
// differs (the broadcaster's between-round state-modifier §3.2.6 is irrelevant
// to the emitted distribution and is omitted for determinism). Skippable in
// -short.

// DS-played reference, percent by box (doc 08 §3) and median overround (§4).
var dsFirstRef = map[string]map[int]float64{
	"dog8": {1: 12.47, 2: 12.48, 3: 12.50, 4: 12.66, 5: 12.43, 6: 12.76, 7: 12.54, 8: 12.18},
	"dog6": {1: 16.55, 2: 17.06, 3: 16.75, 4: 16.44, 5: 16.67, 6: 16.54},
}
var dsSecondRef = map[string]map[int]float64{
	"dog8": {1: 12.16, 2: 12.47, 3: 12.48, 4: 12.46, 5: 12.61, 6: 12.68, 7: 12.63, 8: 12.52},
	"dog6": {1: 16.44, 2: 16.60, 3: 16.97, 4: 16.43, 5: 17.03, 6: 16.53},
}
var dsOverroundRef = map[string]float64{"dog8": 1.1457, "dog6": 1.1613}

// DS prize-multiplier rates, fraction (doc 08 §4.5). P(×3), P(×2); P(×1)=rest.
var dsBonusRef = map[string]struct{ p2x, p3x float64 }{
	"dog8": {p2x: 0.0468, p3x: 0.0084},
	"dog6": {p2x: 0.0470, p3x: 0.00825},
}

func TestGeneratedMatchesDS(t *testing.T) {
	if testing.Short() {
		t.Skip("DS parity battery skipped in -short")
	}
	const (
		sampleN = 200_000
		// Generous guards: emitted output tracks our config targets. Since the
		// 2026-05-31 dog6 uniform recalibration the worst emitted finish drift
		// is ~0.6pp (doc 08 §5); 2.50pp alarms only on a real regression.
		// Overround is calibrated to ~0.0005 of DS, so 0.01 is comfortable
		// headroom over sampling noise. Bonus rates are ~0.04pp from DS; 0.40pp
		// covers binomial noise at N=200k (σ≈0.05pp for a ~4.7% rate).
		finishTolPP  = 2.50
		overroundTol = 0.01
		bonusTolPP   = 0.40
	)

	for _, gt := range []string{"dog8", "dog6"} {
		cfg := mustConfig(t, gt)
		sel := realSelector(t, gt)
		mt := mustMT(t)
		jp := freshState()
		slot := fixedSlot()

		emFirst := make(map[int]int)
		emSecond := make(map[int]int)
		boxOddsSum := make([]float64, cfg.WinOddsCount)
		var ovSum float64
		var n2x, n3x int

		for i := 0; i < sampleN; i++ {
			g, err := GenerateGame(mt, cfg, sel, jp, slot, nil, nil)
			if err != nil {
				t.Fatalf("%s game %d: %v", gt, i, err)
			}
			emFirst[g.Finish.First]++
			emSecond[g.Finish.Second]++
			switch g.Bonus {
			case 2:
				n2x++
			case 3:
				n3x++
			}
			var ov float64
			for b := 0; b < cfg.WinOddsCount; b++ {
				ov += 1.0 / g.Odds[b]
				boxOddsSum[b] += g.Odds[b]
			}
			ovSum += ov
		}

		t.Logf("=== %s  (N=%d generated rounds) ===", gt, sampleN)
		compareEmittedToDS(t, gt, "1º", emFirst, dsFirstRef[gt], sampleN, finishTolPP)
		compareEmittedToDS(t, gt, "2º", emSecond, dsSecondRef[gt], sampleN, finishTolPP)

		genOv := ovSum / float64(sampleN)
		dsOv := dsOverroundRef[gt]
		if d := math.Abs(genOv - dsOv); d > overroundTol {
			t.Errorf("%s overround: generado %.4f vs DS %.4f, Δ=%.4f > %.4f", gt, genOv, dsOv, d, overroundTol)
		} else {
			t.Logf("%s overround: generado %.4f vs DS %.4f, Δ=%.4f OK", gt, genOv, dsOv, d)
		}

		bref := dsBonusRef[gt]
		compareBonusToDS(t, gt, "×2", n2x, bref.p2x, sampleN, bonusTolPP)
		compareBonusToDS(t, gt, "×3", n3x, bref.p3x, sampleN, bonusTolPP)
	}
}

func compareBonusToDS(t *testing.T, gt, label string, count int, dsFrac float64, n int, tolPP float64) {
	t.Helper()
	emPct := 100 * float64(count) / float64(n)
	dsPct := 100 * dsFrac
	if d := math.Abs(emPct - dsPct); d > tolPP {
		t.Errorf("%s bonus %s: generado %.2f%% vs DS %.2f%%, Δ=%.2fpp > %.2fpp", gt, label, emPct, dsPct, d, tolPP)
	} else {
		t.Logf("%s bonus %s: generado %.2f%% vs DS %.2f%%, Δ=%.2fpp OK", gt, label, emPct, dsPct, d)
	}
}

func compareEmittedToDS(t *testing.T, gt, label string, counts map[int]int, ds map[int]float64, n int, tolPP float64) {
	t.Helper()
	var maxDev float64
	var maxBox int
	for box, dsv := range ds {
		emPct := 100 * float64(counts[box]) / float64(n)
		if d := math.Abs(emPct - dsv); d > maxDev {
			maxDev, maxBox = d, box
		}
	}
	emAtMax := 100 * float64(counts[maxBox]) / float64(n)
	if maxDev > tolPP {
		t.Errorf("%s %s: deriva máx %.2fpp en box %d (generado %.2f vs DS %.2f) > %.2fpp",
			gt, label, maxDev, maxBox, emAtMax, ds[maxBox], tolPP)
	} else {
		t.Logf("%s %s: deriva máx %.2fpp en box %d (generado %.2f vs DS %.2f) OK",
			gt, label, maxDev, maxBox, emAtMax, ds[maxBox])
	}
}
