package config

import (
	"math"
	"testing"
)

// TestDSReferenceDrift guards our generator config against drifting from the
// REAL DS vendor distribution, measured over the Elastic collector indices and
// pinned in docs/racegen-design/08-DS-REFERENCE-STATS.md. Jorge's intent:
// "igualar nuestro algoritmo al de DS y no desviarnos". This is a regression
// alarm, not a hard spec — it logs the actual drift every run and only FAILS if
// we move further than a deliberately generous guard, so a future calibration
// change that worsens DS-fit gets caught.
//
// Source ES|QL (run against the per-type vgcontrol-collector-* index):
//
//	FROM <index> | WHERE wsMsgType == "gameResult"
//	| GROK rawMessage "\"competitorIndex\":%{INT:p1}.*?\"competitorIndex\":%{INT:p2}"
//	| WHERE p2 IS NOT NULL | STATS n = COUNT(*) BY p1   (and BY p2)
//
//	FROM <index> | WHERE msgType == "gameRound" AND oddsStr IS NOT NULL
//	| GROK oddsStr "%{NUMBER:o1:double},...,%{NUMBER:oN:double},"
//	| EVAL ov = 1/o1+...+1/oN | STATS PERCENTILE(ov,50)

// DS-played first/second place distribution, percent by box (08 §3).
var dsFirstPlace = map[string]map[int]float64{
	"dog8": {1: 12.47, 2: 12.48, 3: 12.50, 4: 12.66, 5: 12.43, 6: 12.76, 7: 12.54, 8: 12.18},
	"dog6": {1: 16.55, 2: 17.06, 3: 16.75, 4: 16.44, 5: 16.67, 6: 16.54},
}
var dsSecondPlace = map[string]map[int]float64{
	"dog8": {1: 12.16, 2: 12.47, 3: 12.48, 4: 12.46, 5: 12.61, 6: 12.68, 7: 12.63, 8: 12.52},
	"dog6": {1: 16.44, 2: 16.60, 3: 16.97, 4: 16.43, 5: 17.03, 6: 16.53},
}

// DS median overround per type (08 §4).
var dsOverround = map[string]float64{
	"dog8": 1.1457,
	"dog6": 1.1613,
}

func TestDSReferenceDrift(t *testing.T) {
	// Generous guards: after the 2026-05-31 dog6 uniform recalibration the
	// worst finish drift is ~0.6pp (dog8 1º box6 / dog6 2º box5, see 08 §5).
	// 2.50pp leaves headroom but alarms on a real regression.
	const finishTolPP = 2.50
	const overroundTol = 0.005

	for _, gt := range []string{"dog8", "dog6"} {
		cfg, err := Get(gt)
		if err != nil {
			t.Fatalf("%s: %v", gt, err)
		}

		checkMarginal(t, gt, "1º", normalize(cfg.TargetFirstPlace), dsFirstPlace[gt], finishTolPP)
		checkMarginal(t, gt, "2º", normalize(cfg.TargetSecondPlace), dsSecondPlace[gt], finishTolPP)

		if d := math.Abs(cfg.OverroundTarget - dsOverround[gt]); d > overroundTol {
			t.Errorf("%s overround: config %.4f vs DS %.4f, Δ=%.4f > %.4f",
				gt, cfg.OverroundTarget, dsOverround[gt], d, overroundTol)
		} else {
			t.Logf("%s overround: config %.4f vs DS %.4f, Δ=%.4f OK", gt, cfg.OverroundTarget, dsOverround[gt], d)
		}
	}
}

// normalize rescales a percent map so it sums to 100 (dog6 TargetFirstPlace
// sums to ≈116.18 by design — the overround value; IPF normalizes internally).
func normalize(m map[int]float64) map[int]float64 {
	var sum float64
	for _, v := range m {
		sum += v
	}
	out := make(map[int]float64, len(m))
	for k, v := range m {
		out[k] = 100 * v / sum
	}
	return out
}

func checkMarginal(t *testing.T, gt, label string, cfgPct, ds map[int]float64, tolPP float64) {
	t.Helper()
	var maxDev float64
	var maxBox int
	for box, dsv := range ds {
		d := math.Abs(cfgPct[box] - dsv)
		if d > maxDev {
			maxDev, maxBox = d, box
		}
	}
	if maxDev > tolPP {
		t.Errorf("%s %s: max drift %.2fpp at box %d (config %.2f vs DS %.2f) > %.2fpp guard — recalibrar hacia DS",
			gt, label, maxDev, maxBox, cfgPct[maxBox], ds[maxBox], tolPP)
	} else {
		t.Logf("%s %s: max drift %.2fpp at box %d (config %.2f vs DS %.2f) OK",
			gt, label, maxDev, maxBox, cfgPct[maxBox], ds[maxBox])
	}
}
