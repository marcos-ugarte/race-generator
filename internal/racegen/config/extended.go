// Package config holds the extended GameTypeConfig registry consumed by
// the racegen generator. Each entry encodes the full set of calibrated
// parameters (odds calibration, IPF targets, finish/video shapes, init
// payload knobs, weather, bonus probabilities) for one game type.
//
// Layout mirrors virteon-platform/packages/game-engine/src/config/game-types.ts
// — the legacy TypeScript registry — so values are eyeball-comparable.
// Values are copied verbatim from that source unless explicitly noted in
// per-field comments.
//
// Phase 2 shipped dog8 + dog6. horse_classic (betoffer 241) added 2026-06-09
// with a real DS finish pool but SMOKE-LEVEL odds calibration (see
// horseClassicConfig doc — GLI gate stays closed for 241). horse (251) and
// dog63 follow in later phases.
package config

import (
	"fmt"
	"math"
)

// ----------------------------------------------------------------------------
// Building-block types
// ----------------------------------------------------------------------------

// PositionConstraint defines a truncated-Normal draw for an odds at one
// finishing position (1st-place odds, 2nd-place odds, …).
type PositionConstraint struct {
	Mean, Std, Min, Max float64
}

// Range is an inclusive [Min, Max] float interval.
type Range struct{ Min, Max float64 }

// OddsFinishCoupling controls how strongly the WIN odds VALUE multiset is
// coupled to the already-chosen finish order. A Mallows model (via the
// Repeated Insertion Model) maps finish-rank → odds-value-rank using the
// single dispersion parameter Theta:
//
//   - Theta == 0 ⇒ uniform assignment ⇒ legacy behavior (odds independent
//     of the winner; P(win|odds-rank) flat at 1/N).
//   - Theta  > 0 ⇒ earlier finishers tend to get lower odds, so favorites
//     (lowest odds) win more — matching the vendor (DS) favorite-bias.
//
// Only the ASSIGNMENT changes; the per-round odds VALUE multiset is
// preserved, so overround / RTP and per-index marginals are unaffected.
//
// UsePL switches the assignment from the single-theta Mallows RIM to a
// Plackett-Luce (PL) model parameterised by Weights (length n, favorite→
// longshot rank order). Mallows fits the MARGINAL P(win|rank) but produces a
// near-FLAT conditional P(2nd-rank|1st-rank); the vendor (DS) conditional is
// strongly tilted (after the favorite wins, the runner-up is most likely the
// 2nd-favorite, etc.). DS's joint (1st,2nd)-rank distribution is matched within
// ~0.5pp by a PL whose weights are the DS win marginal itself: sequential
// sampling-without-replacement gives P(1st=a)=w_a/Σw AND
// P(2nd=b|1st=a)=w_b/(Σw−w_a). PL with these weights therefore reproduces the
// SAME favorite-win marginal as Mallows while also fixing the conditional — the
// lever behind the residual exacta-cell +EV (see test/parity_exacta). Theta is
// retained as the fallback when UsePL is false.
type OddsFinishCoupling struct {
	Theta float64
	// UsePL selects the Plackett-Luce assignment over the Mallows RIM.
	UsePL bool
	// Weights is the PL weight vector in finish-rank order (index 0 = favorite),
	// length n. Used only when UsePL is true.
	Weights []float64
}

// validate checks the PL weight vector when UsePL is set.
func (c OddsFinishCoupling) validate(n int) error {
	if !c.UsePL {
		return nil
	}
	if len(c.Weights) != n {
		return fmt.Errorf("OddsFinishCoupling.Weights len %d, want %d", len(c.Weights), n)
	}
	var sum float64
	for i, w := range c.Weights {
		if w <= 0 {
			return fmt.Errorf("OddsFinishCoupling.Weights[%d]=%g, must be > 0", i, w)
		}
		sum += w
	}
	if sum <= 0 {
		return fmt.Errorf("OddsFinishCoupling.Weights sum %g, must be > 0", sum)
	}
	return nil
}

// RankGapModel generates the WIN-odds vector ALREADY SORTED (ascending,
// favorite→longshot) by sampling the adjacent gaps between consecutive
// sorted ranks, instead of drawing each finishing position independently
// and sorting after coupling. This gives direct control over the
// favorite↔runner-up gap distribution — the lever that fixes the
// within-matrix odds-repeat (1dp tie) rate, which the per-position
// marginal-Std lever cannot reach (the tie rate is a joint lower-tail
// order-statistic property, and the multiplicative overround rescale
// strips any additive common factor). See docs/racegen-design/09-ODDS-REPEAT-RATE.md.
//
// Enabled=false ⇒ no-op: generateWinOdds uses the per-position HEAD path.
//
// Construction (per attempt): s[0] is the favorite anchor drawn from
// PositionConstraints[0]; for r in 1..n-1, s[r] = s[r-1] + max(0,
// Normal-clamped(GapMean[r-1], GapStd[r-1], GapMin[r-1], GapMax[r-1])), so
// s is non-decreasing by construction. The vector then flows through the
// SAME shape-concentration, multiplicative overround rescale, clamp, round,
// and odds↔finish coupling as the HEAD path — the rescale preserves order
// and relative gaps, so the sorted multiset handed to coupleWinOdds is
// statistically equivalent in everything except the lower-tail gap shape.
type RankGapModel struct {
	Enabled bool
	// GapMean/GapStd/GapMin/GapMax each have length n-1: entry r is the
	// truncated-Normal draw for the gap between sorted rank r and r+1.
	GapMean []float64
	GapStd  []float64
	GapMin  []float64
	GapMax  []float64
	// OddsFloor is the global lower clamp on the favorite after rescale.
	OddsFloor float64
	// LongshotMaxOdds is a soft-cap below MaxOdds: tail values above it are
	// compressed into [LongshotMaxOdds, MaxOdds] instead of piling up against
	// the hard MaxOdds clamp (which would over-tie longshots at 1dp).
	LongshotMaxOdds float64
}

// ForecastRankFactor makes the forecast (ordered-pair / exacta) combined
// factor RANK-AWARE. The vendor (DS) does not price the forecast pair with a
// flat factor: pairs LED BY THE FAVORITE are priced shorter than win_i·win_j
// (factor < 1) and the factor scales monotonically up toward the longshot-led
// pairs (> 1). Measured per-rank over Elastic (ds_exacta_sig.py): dog8
// favorite-led ≈0.92 → longshot-led ≈1.08; dog6 ≈0.88 → ≈1.06. A flat factor
// (the legacy CombinedFactor, calibrated only to the GLOBAL average because it
// was measured per physical SLOT — which is uncorrelated with rank after the
// odds↔finish coupling, so the tilt averaged out) prices the favorite-led
// exacta ~8-12% too long, which is +EV for a player betting favorite-first
// exactas. This model restores the tilt while preserving the global mean
// factor, so the per-slot forecast parity (parity_odds) is unchanged.
//
// Enabled=false ⇒ generateForecastOdds falls back to the flat CombinedFactor
// (byte-identical to legacy: same single CertifiedNormalClamped draw per pair).
//
// Mean has length n (WinOddsCount): entry r is the factor MEAN applied to every
// ordered pair whose FIRST runner (slot i) has sorted-ascending odds rank r
// (0=favorite). The rank_j (second runner) dependence DS shows is second-order
// (<0.5% within a row) and folded into the rank_i row-mean. Std/Min/Max are the
// shared per-pair noise band (copied from CombinedFactor — the spread already
// matched DS p10-p90; only the per-rank center changes). The arithmetic mean of
// Mean must equal CombinedFactor.Mean (each rank_i appears in exactly n-1 of the
// n(n-1) pairs, so the global factor = simple mean of Mean) — this is what keeps
// parity_odds green and is asserted by a unit test.
type ForecastRankFactor struct {
	Enabled bool
	Mean    []float64
	Std     float64
	Min     float64
	Max     float64
}

// IntRange is an inclusive [Min, Max] integer interval.
type IntRange struct{ Min, Max int }

// BetType describes one slice of the odds vector for a given betoffer.
// OddsIndexStart and OddsIndexEnd are inclusive into the per-config odds
// array (length == NumberOdds).
type BetType struct {
	BettypeID      int
	BettypeName    string
	OddsIndexStart int
	OddsIndexEnd   int
	OddsDecimals   int
}

// ----------------------------------------------------------------------------
// GameTypeConfigExt — the full calibrated descriptor
// ----------------------------------------------------------------------------

// GameTypeConfigExt is the extended game-type configuration. Phase 3+
// generators consume an immutable value pulled from the registry via
// Get(gameType).
type GameTypeConfigExt struct {
	// Identity
	GameType   string
	BetofferID int
	ScheduleID int
	EventType  string

	// Competitors
	NumberCompetitor int
	NumberWinner     int

	// Odds (5000-sample calibration)
	NumberOdds         int
	WinOddsCount       int
	ForecastOddsCount  int
	OverroundTarget    float64
	OverroundTolerance float64
	// OverroundShift / OverroundLogSigma model the PER-RACE overround as a
	// shifted-lognormal: ov = OverroundShift + (OverroundTarget-OverroundShift)
	// * exp(OverroundLogSigma * Z), Z ~ certified N(0,1). This reproduces the
	// real DS spread (the vendor varies its margin race-to-race; dog8 is
	// right-skewed with a tail to ~1.17, dog6 wider/near-symmetric) instead of
	// clamping every race to a single target. Median = OverroundTarget by
	// construction. OverroundLogSigma==0 disables it (fixed-target legacy).
	OverroundShift    float64
	OverroundLogSigma float64
	// ShapeConcentrationSigma models the PER-RACE SHAPE variance the vendor
	// shows: DS does not draw the same odds shape every race — ~5% of dog8
	// races have a strong favorite (≤3.0) with a wide field (longshot ~15.7),
	// ~12% are flat (favorite ≥5.0, longshot ~13.3), the rest in between, all
	// at the SAME overround (~1.146). We reproduce this by drawing a per-race
	// log-normal concentration kappa = exp(ShapeConcentrationSigma*Z), Z ~
	// certified N(0,1) clamped, and stretching/compressing the raw odds in log
	// space around their geometric mean: odds_i = G*(odds_i/G)^kappa. kappa>1
	// concentrates (favorite shorter, longshot longer); kappa<1 flattens. The
	// overround rescale that follows re-normalizes the level, so the margin is
	// preserved while the shape varies. ==0 disables it (legacy fixed shape).
	//
	// ShapeConcentrationSigma is the CONCENTRATE-side sigma (Z>0 ⇒ kappa>1).
	// ShapeConcentrationSigmaLo, when >0, is the FLATTEN-side sigma (Z<0 ⇒
	// kappa<1) — a split-lognormal. DS dog8 is asymmetric: the strong-favorite
	// tail (fav≤3.0, ~5%) is heavier than the flat tail (fav≥5.0, ~12% but
	// shallow), so we concentrate harder than we flatten. ==0 ⇒ symmetric
	// (uses ShapeConcentrationSigma for both sides).
	ShapeConcentrationSigma   float64
	ShapeConcentrationSigmaLo float64
	MaxOdds                   float64
	PositionConstraints       []PositionConstraint
	CombinedFactor            PositionConstraint

	// OddsFinishCoupling couples WIN-odds-value assignment to the chosen
	// finish order (favorite-win bias). Theta=0 reproduces legacy uniform
	// assignment. See OddsFinishCoupling doc.
	OddsFinishCoupling OddsFinishCoupling

	// RankGap, when Enabled, generates WIN odds in rank-space via adjacent
	// gaps to match the DS within-matrix odds-repeat rate. No-op when
	// disabled. See RankGapModel doc.
	RankGap RankGapModel

	// ForecastRank, when Enabled, makes the forecast combined factor depend on
	// the sorted rank of the first runner (favorite-led pairs priced shorter),
	// matching the DS rank-aware exacta pricing. No-op when disabled (flat
	// CombinedFactor). See ForecastRankFactor doc.
	ForecastRank ForecastRankFactor

	// Finish / Videos
	VideoPoolPath    string // "dog8" / "dog6" — matches data.VideoPool key
	VideoFileSuffix  string // e.g. "_h", "_h50"
	VideoIDPrefix    string // "DOG8", "DOG6"
	VideoNameLiteral bool   // true: pool IDs ARE the real video stem (horse_classic 7-digit) — skip R%04d
	FinishTimeRange  Range
	GapRange         Range
	GapExponent      float64
	IntervalCount    int

	// IPF targets — percentages.
	//
	// Most game types use values that sum to ≈100. dog6 ships with
	// targetFirstPlace summing to ≈116.18 (verbatim from the legacy TS
	// at packages/game-engine/src/config/game-types.ts L278). The IPF
	// algorithm normalizes internally, so absolute magnitude doesn't
	// affect outputs — only the ratios matter. Validate() therefore
	// requires positive entries with a strictly positive sum, not a
	// strict sum-to-100.
	TargetFirstPlace  map[int]float64
	TargetSecondPlace map[int]float64

	// IPF tuning (videoselector). Zero/empty → defaults applied at use
	// site (videoselector.New): 50 iterations, 0.2 exacta correction.
	//
	// Tuning rationale: the legacy virteon-platform IPF (iterations=50,
	// exactaFactor=0.2) over-amplifies video-pool weights compared to the
	// vendor DS reference. dog8 specifically exhibits CoV 0.62 vs vendor
	// 0.45 (~37% more skewed). Reducing iterations or raising the exacta
	// factor smooths the distribution. dog6 already aligns at defaults
	// (CoV 0.26 vs vendor 0.28), so it keeps the legacy values.
	// See test/parity_odds/results/full_audit_2026-05-20.md §5.
	IPFIterations   int     // default 50 (legacy)
	IPFExactaFactor float64 // default 0.2 (legacy)

	// Finish structure (legacy lines 79–90)
	TimedFinishPositions int
	IntervalSentinel     bool
	IntervalProportional bool
	Interval1TimeRange   Range
	Interval2TimeRange   Range
	IntervalRatio1       float64 // horse-only; 0 for dogs
	IntervalRatio2       float64
	IntervalRatioNoise   float64

	// Timing
	RoundIntervalSec  int
	VideoDurationSec  int    // length of the race video itself (videoEnd-videoStart); matches DS (dogs 45s, horse 40s). The rest of RoundIntervalSec is the betting/countdown gap.
	ScheduleStartTime string // "HH:MM:SS" — adapter uses for epoch

	// Competitor stats
	WeightRange      Range
	StrikeRateRange  Range
	BestLapRange     Range
	PerformanceRange Range
	NumberOfWinsMax  int

	// Bonus
	Bonus2xProbability float64
	Bonus3xProbability float64

	// Init / response shape
	SkinVersion  int
	VideoQuality string
	LanguageID   string
	RTP          float64
	BetTypes     []BetType

	// Weather
	WeatherOptions   []string
	WeatherWeights   []float64 // sum ≈ 1.0
	TemperatureRange IntRange
	HumidityRange    IntRange
	WindSpeedRange   IntRange
}

// ----------------------------------------------------------------------------
// Per-type constructors
// ----------------------------------------------------------------------------

// dog8Config builds the dog8 configuration. Values copied verbatim from
// packages/game-engine/src/config/game-types.ts L108–L209.
//
// Calibration: 5,000 production samples, January 2026.
func dog8Config() GameTypeConfigExt {
	return GameTypeConfigExt{
		// Identity
		GameType:   "dog8",
		BetofferID: 541,
		ScheduleID: 105,
		EventType:  "dog8",

		// Competitors
		NumberCompetitor: 8,
		NumberWinner:     8,

		// Odds
		NumberOdds:        64,
		WinOddsCount:      8,
		ForecastOddsCount: 56,
		// OverroundTarget recalibrated 2026-05-21 (was 1.1723 — RTP 85.30%).
		// Vendor measured Winner RTP over 5000 dog8 rounds = 87.27% → target
		// overround = 1/0.8727 = 1.1459. Per audit §2 P3. The legacy 1.1723
		// value derived from a different calibration source; vendor reality
		// is closer to 1.1459. dog6 stays at 1.1618 (already aligned).
		OverroundTarget:    1.1459,
		OverroundTolerance: 0.003,
		// Per-race overround spread fitted to DS dog8 (46,636 rounds, Elastic
		// 2026-05-31): median 1.1457, p05 1.1419, p95 1.1526, p99 1.170 —
		// right-skewed. shift 1.140 + logσ 0.482 reproduces p05≈1.143/
		// p95≈1.153 and a real right tail (see doc 08 §4.6).
		OverroundShift:    1.140,
		OverroundLogSigma: 0.482,
		// Split-lognormal shape concentration (2026-06-01): DS dog8 favorite is
		// left-skewed (heavier strong-favorite tail than flat tail), so we
		// concentrate harder (σ_hi 0.24) than we flatten (σ_lo 0.17). This is the
		// per-race shape variance the vendor shows; with the favorite Std at 0.75
		// it lets the favorite REACH the DS extremes (min 1.8, longshot cap 17.5)
		// while keeping rank means and the fav≥5.0 band (12.1% vs DS 11.8%)
		// clavados. KNOWN RESIDUAL: the fav<3.0 band lands ~0.9% vs DS ~5.1% —
		// DS's favorite is mildly BIMODAL (tight 4-5 core + a distinct strong-
		// favorite excursion that jumps past 3-4 to <3.0), which a unimodal-
		// favorite+kappa model can approximate but not fully reproduce without an
		// explicit strong-favorite mixture (deferred — odds are not a cert gate).
		ShapeConcentrationSigma:   0.24,
		ShapeConcentrationSigmaLo: 0.17,
		MaxOdds:                   17.5,
		// Per-sorted-rank WIN-odds means fitted to DS dog8 (Elastic 2026-05-31:
		// 4.36/5.14/5.82/6.79/8.03/9.75/11.87/14.41). Favorite re-tuned 2026-06-01
		// to Mean 4.55 / Std 0.75 / Min 1.8 / Max 6.3 alongside the split-lognormal
		// shape concentration above: the wider Std lets the favorite reach DS's low
		// extreme (output min 1.8, p50 4.4) while κ supplies the per-race shape
		// spread. With the per-race overround draw + κ in place the WIN rank means
		// all land within ≤0.12 of DS (4.34/5.08/5.74/6.69/7.98/9.76/11.99/14.37
		// over N=10k). See the ShapeConcentration note for the fav<3.0 residual.
		//
		// Under the rank-space gap model (RankGap below, 2026-06-02) only
		// PositionConstraints[0] (the favorite anchor) is consumed by WIN-odds
		// generation; ranks 1..n-1 are produced from the sampled adjacent gaps,
		// so the Std/Min/Max of entries 1..7 are NOT used at runtime (they remain
		// as documentation of the DS per-sorted-rank marginals the gap means are
		// calibrated to reproduce). A 2026-06-02 attempt to widen these Stds to
		// close the within-matrix tie rate was REVERTED — the marginal-Std lever
		// is structurally unable to reach the joint lower-tail tie rate (the
		// multiplicative overround rescale strips the effect); the rank-space gap
		// model replaces it. Values are the pre-widening calibration.
		PositionConstraints: []PositionConstraint{
			{Mean: 4.55, Std: 0.75, Min: 1.8, Max: 6.3},
			{Mean: 5.14, Std: 0.49, Min: 4.3, Max: 8.4},
			{Mean: 5.82, Std: 0.65, Min: 4.4, Max: 8.8},
			{Mean: 6.79, Std: 0.83, Min: 4.9, Max: 10.0},
			{Mean: 8.03, Std: 1.16, Min: 5.9, Max: 13.0},
			{Mean: 9.75, Std: 1.67, Min: 6.4, Max: 16.7},
			{Mean: 11.87, Std: 2.11, Min: 7.5, Max: 17.3},
			{Mean: 14.41, Std: 1.97, Min: 8.0, Max: 17.5},
		},
		// CombinedFactor re-fitted 2026-06-01 to the DS forecast combined-factor
		// f/(wi*wj), measured pair-independent (flat across all 56 cells) over
		// 46,644 DS dog8 rounds (Elastic): mean 1.0088, p50 1.014, p10 0.936,
		// p90 1.077 → Mean 1.009 makes our forecast matrix indistinguishable
		// from DS (prior 0.992 ran ~1.6% short on every combined). Std/Min/Max
		// preserved (spread p10–p90 already matched DS). See doc 08 §4.6.
		CombinedFactor: PositionConstraint{Mean: 1.009, Std: 0.0567, Min: 0.85, Max: 1.15},

		// ForecastRank: rank-aware forecast factor. DS prices the exacta pair by
		// the rank of the first runner — favorite-led ≈0.92, scaling to
		// longshot-led ≈1.08. Measured as per-rank_i row-means of the DS
		// rank-pair ratio matrix over 27,153 dog8 rounds (Elastic
		// vgcontrol-collector-dog8-27bb, ds_exacta_sig.py 2026-06-02). Row-means
		// [0.9181,0.9564,0.9777,1.0000,1.0229,1.0439,1.0626,1.0773] (mean
		// 1.0074) shifted +0.0016 to center on CombinedFactor.Mean=1.009 so the
		// per-slot forecast parity (parity_odds) is preserved. Std/Min/Max from
		// CombinedFactor (DS p10-p90 band unchanged). See doc 08 §4.6(c).
		ForecastRank: ForecastRankFactor{
			Enabled: true,
			Mean:    []float64{0.9197, 0.9580, 0.9793, 1.0016, 1.0245, 1.0455, 1.0642, 1.0789},
			Std:     0.0567, Min: 0.85, Max: 1.15,
		},

		// OddsFinishCoupling.Theta calibrated 2026-05-30. Couples WIN-odds
		// assignment to the chosen finish order so favorites win at the
		// vendor (DS) rate. DS over 20000 dog8 rounds: P(win|odds-rank) =
		// [21.00,16.99,15.07,13.16,10.66,9.18,7.49,6.43]%, winner/field
		// odds ratio 0.8421. Theta=0.18 fits BOTH joint-parity gates via the
		// Mallows RIM, swept against the real odds path (worst per-rank
		// |gen−DS| ≈ 1.00pp ≤ 1.5pp gate; winner/field ratio 0.8530, |Δ| ≈
		// 0.011 ≤ 0.02 gate; see test/parity_joint -strict). A higher
		// Theta=0.20 sharpens the ratio (Δ≈0.003) but breaches the rank gate
		// (1.65pp); 0.18 is the joint optimum. Theta=0 reproduces legacy
		// uniform assignment.
		OddsFinishCoupling: OddsFinishCoupling{
			Theta: 0.18,
			UsePL: true,
			// PL weights = DS win marginal P(1st=rank) (ds_secondplace.py,
			// 27.2k joined dog8). PL reproduces this marginal AND the DS
			// conditional P(2nd|1st) the flat Mallows missed.
			Weights: []float64{20.86, 17.06, 15.20, 13.16, 10.61, 9.34, 7.44, 6.33},
		},

		// RankGap (2026-06-02): generate WIN odds in rank-space via adjacent
		// gaps to match the DS within-matrix 1dp tie rate (P(matrix has tie) DS
		// 26.70%). For each sorted pair the gap is a Normal(GapMean,GapStd)
		// truncated to [GapMin,GapMax]; the NEGATIVE tail [GapMin,0) is folded to
		// gap=0 by max(0,·), so P(gap<0) becomes an exact-tie point-mass. GapMean
		// /GapStd are solved per pair from two targets: P(gap<0)=DS adjacent-tie
		// rate (7.86/6.32/4.92/3.50/3.14/1.84/1.64 %) and E[max(0,gap)]=DS mean
		// rank gap (0.76/0.66/0.92/1.28/1.72/2.26/2.52). GapMin=-1.0 captures the
		// whole negative tail (rejection negligible). LongshotMaxOdds soft-caps
		// the tail below MaxOdds so longshots don't over-tie at the hard clamp.
		// See doc 09 §4.
		RankGap: RankGapModel{
			Enabled:         true,
			GapMean:         []float64{0.75, 0.66, 0.93, 1.31, 1.77, 2.34, 2.62},
			GapStd:          []float64{0.53, 0.41, 0.53, 0.67, 0.89, 1.05, 1.15},
			GapMin:          []float64{-1.0, -1.0, -1.0, -1.0, -1.0, -1.0, -1.0},
			GapMax:          []float64{3, 3, 4, 5, 6, 7, 9},
			OddsFloor:       1.8,
			LongshotMaxOdds: 17.0,
		},

		// Finish / Videos
		VideoPoolPath:   "dog8",
		VideoFileSuffix: "_h",
		VideoIDPrefix:   "DOG8",
		FinishTimeRange: Range{Min: 28.54, Max: 30.12},
		GapRange:        Range{Min: 0.01, Max: 0.60},
		GapExponent:     2.7,
		IntervalCount:   2,

		// IPF targets — percentages, sum ≈ 99.99
		TargetFirstPlace: map[int]float64{
			1: 12.96, 2: 13.00, 3: 11.92, 4: 12.26,
			5: 11.99, 6: 13.32, 7: 12.62, 8: 11.92,
		},
		TargetSecondPlace: map[int]float64{
			1: 12.52, 2: 12.09, 3: 12.58, 4: 12.71,
			5: 12.56, 6: 12.64, 7: 13.15, 8: 11.75,
		},

		// IPF tuning — diverges from legacy virteon defaults (50/0.2)
		// to converge our video distribution toward vendor DS reference
		// (CoV ≈ 0.45, max/min ratio ≈ 17.9×). With legacy 50/0.2 we
		// over-amplify (CoV ≈ 0.59); with 10/0.20 we reach CoV ≈ 0.55,
		// ratio ≈ 16.6× — the best balance of CoV and ratio per the sweep
		// in test/parity_videos/sweep. See full_audit_2026-05-20.md §5.
		IPFIterations:   10,
		IPFExactaFactor: 0.20,

		// Finish structure
		TimedFinishPositions: 3,
		IntervalSentinel:     false,
		IntervalProportional: false,
		Interval1TimeRange:   Range{Min: 7.50, Max: 7.89},
		Interval2TimeRange:   Range{Min: 23.13, Max: 24.13},
		IntervalRatio1:       0, // dogs don't use proportional intervals
		IntervalRatio2:       0,
		IntervalRatioNoise:   0,

		// Timing
		RoundIntervalSec:  240,
		VideoDurationSec:  60, // dog8 mp4 real length (race + result); the race must stay LIVE the whole clip so the overlays + winner show
		ScheduleStartTime: "23:03:30",

		// Competitor stats
		WeightRange:      Range{Min: 25.0, Max: 40.0},
		StrikeRateRange:  Range{Min: 10.1, Max: 19.9},
		BestLapRange:     Range{Min: 30.0, Max: 39.9},
		PerformanceRange: Range{Min: 0.31, Max: 0.75},
		NumberOfWinsMax:  5,

		// Bonus — empirical from vendor measured over 39528 dog8 gameResults:
		// bonus=2 appears 4.70% (was 4.30%), bonus=3 appears 0.86% (was 0.60%).
		// Updated 2026-05-21 per audit §3 P2; legacy values were inherited
		// from virteon-platform but skew slightly low vs vendor production.
		Bonus2xProbability: 0.047,
		Bonus3xProbability: 0.0086,

		// Init / response
		SkinVersion:  11,
		VideoQuality: "crf27",
		LanguageID:   "es",
		RTP:          0.87,
		BetTypes: []BetType{
			{BettypeID: 1, BettypeName: "Winner", OddsIndexStart: 0, OddsIndexEnd: 7, OddsDecimals: 1},
			{BettypeID: 2, BettypeName: "Forecast in order", OddsIndexStart: 8, OddsIndexEnd: 63, OddsDecimals: 1},
		},

		// Weather
		WeatherOptions:   []string{"fine", "cloudy", "sunny"},
		WeatherWeights:   []float64{0.411, 0.310, 0.279},
		TemperatureRange: IntRange{Min: 10, Max: 26},
		HumidityRange:    IntRange{Min: 60, Max: 80},
		WindSpeedRange:   IntRange{Min: 1, Max: 15},
	}
}

// dog6Config builds the dog6 configuration. Most values copied verbatim from
// packages/game-engine/src/config/game-types.ts L215–L296.
//
// Calibration: 1,159 production samples, February 2026.
//
// NB: legacy EventType is "dog" (not "dog6") — kept for protocol compat.
// NB: TargetFirstPlace recalibrated 2026-05-31 to UNIFORM (16.67/box) to
//
//	track the real DS vendor distribution measured over 42,070 distinct
//	dog6 rounds in Elastic (per-box 1º ∈ [16.44, 17.06], statistically
//	flat — see docs/racegen-design/08-DS-REFERENCE-STATS.md §3, §5–§6).
//	The legacy verbatim values (sum ≈116.18, box5 as low as 17.49→15.05
//	normalized) drifted up to 1.6pp from DS; this flattening closes that
//	gap. IPF normalizes shape internally so the absolute scale is moot.
func dog6Config() GameTypeConfigExt {
	return GameTypeConfigExt{
		// Identity
		GameType:   "dog6",
		BetofferID: 141,
		ScheduleID: 101,
		EventType:  "dog",

		// Competitors
		NumberCompetitor: 6,
		NumberWinner:     6,

		// Odds
		NumberOdds:         36,
		WinOddsCount:       6,
		ForecastOddsCount:  30,
		OverroundTarget:    1.1618,
		OverroundTolerance: 0.003,
		// Per-race overround spread fitted to DS dog6 (159,583 clean rounds,
		// Elastic 2026-05-31: median 1.162, p05 1.156, p95 1.168, p99 1.170 —
		// near-symmetric, narrower than dog8). shift 1.150 + logσ 0.246
		// reproduces p05≈1.158/p95≈1.168/p99≈1.171 (see doc 08 §4.6). dog6
		// per-rank means + MaxOdds 12.9 already match DS, so only the spread
		// is added here.
		OverroundShift:    1.150,
		OverroundLogSigma: 0.246,
		// Per-race SHAPE concentration κ (2026-06-01), mirroring dog8 — but dog6's
		// favorite tails are near-SYMMETRIC (DS: fav<2.8 2.65% ≈ fav≥4.2 2.58%),
		// so a single symmetric σ (no σ_lo split) reproduces both. Without κ the
		// favorite collapses to a 3.2-3.8 spike (83.8%) and never reaches DS's
		// 2.2/4.8 extremes; κ spreads it while the overround rescale holds the
		// margin. See doc 08 §4.6.
		ShapeConcentrationSigma: 0.24,
		MaxOdds:                 12.9,
		// Favorite Min/Max widened 2.8/4.0→2.0/5.0 so the favorite anchor's
		// κ-driven spread reaches the DS extremes (fav min 2.2).
		//
		// Under the rank-space gap model (RankGap below, 2026-06-02) only
		// PositionConstraints[0] (favorite anchor) is consumed; ranks 1..5 come
		// from the sampled adjacent gaps, so their Std/Min/Max are unused at
		// runtime (kept as documentation of the DS per-sorted-rank marginals the
		// gap means reproduce). The 2026-06-02 Std widening was REVERTED in favor
		// of the gap model. Values 1..5 are the pre-widening calibration.
		PositionConstraints: []PositionConstraint{
			{Mean: 3.41, Std: 0.25, Min: 2.00, Max: 5.00},
			{Mean: 4.11, Std: 0.30, Min: 3.50, Max: 5.00},
			{Mean: 4.66, Std: 0.40, Min: 3.70, Max: 6.20},
			{Mean: 5.76, Std: 0.55, Min: 4.50, Max: 8.10},
			{Mean: 8.03, Std: 1.20, Min: 5.60, Max: 12.70},
			{Mean: 10.26, Std: 1.50, Min: 6.70, Max: 12.90},
		},
		// CombinedFactor.Mean recalibrated 2026-05-21 from 0.974 to 0.955.
		// At legacy 0.974, Forecast RTP measured 88.51% vs vendor 86.79%
		// — 1.7pp over-generous. At 0.955, RTP converges to ~86.8%.
		// Std/Min/Max bounds preserved. Per audit §1 P4.
		CombinedFactor: PositionConstraint{Mean: 0.955, Std: 0.054, Min: 0.79, Max: 1.11},

		// ForecastRank: rank-aware forecast factor (see dog8 + ForecastRankFactor
		// doc). DS dog6 per-rank_i row-means over 31,598 rounds (Elastic
		// vgcontrol-collector-dog6, ds_exacta_sig.py 2026-06-02):
		// [0.8850,0.9214,0.9540,0.9932,1.0318,1.0620] (mean 0.9746) shifted
		// -0.0196 to center on CombinedFactor.Mean=0.955. Favorite-led ≈0.87 →
		// longshot-led ≈1.04. Std/Min/Max from CombinedFactor.
		ForecastRank: ForecastRankFactor{
			Enabled: true,
			Mean:    []float64{0.8654, 0.9018, 0.9344, 0.9736, 1.0122, 1.0424},
			Std:     0.054, Min: 0.79, Max: 1.11,
		},

		// OddsFinishCoupling.Theta calibrated 2026-05-30. Couples WIN-odds
		// assignment to the chosen finish order so favorites win at the
		// vendor (DS) rate. DS over 20000 dog6 rounds: P(win|odds-rank) =
		// [24.94,21.11,18.45,15.04,11.38,9.09]%, winner/field odds ratio
		// 0.8668. Theta=0.20 fits BOTH joint-parity gates via the Mallows
		// RIM, swept against the real odds path (worst per-rank |gen−DS| ≈
		// 0.95pp ≤ 1.5pp gate; winner/field ratio 0.8797, |Δ| ≈ 0.013 ≤
		// 0.02 gate; see test/parity_joint -strict). A higher Theta=0.22
		// sharpens the ratio (Δ≈0.002) but breaches the rank gate (1.85pp);
		// 0.20 is the joint optimum. Theta=0 reproduces legacy uniform
		// assignment.
		OddsFinishCoupling: OddsFinishCoupling{
			Theta: 0.20,
			UsePL: true,
			// PL weights = DS win marginal P(1st=rank) (ds_secondplace.py,
			// 31.6k joined dog6).
			Weights: []float64{24.97, 21.20, 18.26, 15.23, 11.53, 8.82},
		},

		// RankGap (2026-06-02): rank-space WIN-odds gaps to match DS tie rate
		// (P(matrix has tie) DS 18.64%; adj-tie %: 6.44/5.46/3.60/2.16/1.96;
		// DS mean gaps 0.53/0.66/1.14/1.82/2.36). Same solve as dog8 (negative
		// GapMin folds to an exact-tie point-mass). See dog8 RankGap note + doc 09.
		RankGap: RankGapModel{
			Enabled:         true,
			GapMean:         []float64{0.53, 0.66, 1.16, 1.85, 2.40},
			GapStd:          []float64{0.345, 0.39, 0.60, 0.84, 1.08},
			GapMin:          []float64{-1.0, -1.0, -1.0, -1.0, -1.0},
			GapMax:          []float64{3, 4, 5, 7, 8},
			OddsFloor:       2.0,
			LongshotMaxOdds: 12.5,
		},

		// Finish / Videos
		VideoPoolPath:   "dog6",
		VideoFileSuffix: "_h50",
		VideoIDPrefix:   "DOG6",
		FinishTimeRange: Range{Min: 28.54, Max: 30.12},
		GapRange:        Range{Min: 0.01, Max: 0.60},
		GapExponent:     2.7,
		IntervalCount:   2,

		// IPF targets. firstPlace = uniform DS calibration (2026-05-31, see
		// dog6Config doc comment); secondPlace ≈ 100, legacy verbatim.
		TargetFirstPlace: map[int]float64{
			1: 16.67, 2: 16.67, 3: 16.67, 4: 16.67, 5: 16.67, 6: 16.65,
		},
		TargetSecondPlace: map[int]float64{
			1: 16.67, 2: 16.67, 3: 16.67, 4: 16.67, 5: 16.67, 6: 16.65,
		},

		// Finish structure
		TimedFinishPositions: 3,
		IntervalSentinel:     false,
		IntervalProportional: false,
		Interval1TimeRange:   Range{Min: 7.43, Max: 7.97},
		Interval2TimeRange:   Range{Min: 23.33, Max: 24.12},
		IntervalRatio1:       0,
		IntervalRatio2:       0,
		IntervalRatioNoise:   0,

		// Timing
		RoundIntervalSec:  240,
		VideoDurationSec:  60, // dog6 mp4 real length (race + result)
		ScheduleStartTime: "23:00:00",

		// Competitor stats
		WeightRange:      Range{Min: 25.0, Max: 40.0},
		StrikeRateRange:  Range{Min: 10.1, Max: 19.9},
		BestLapRange:     Range{Min: 30.0, Max: 39.9},
		PerformanceRange: Range{Min: 0.31, Max: 0.75},
		NumberOfWinsMax:  5,

		// Bonus — empirical from vendor measured over 41148 dog6 gameResults:
		// bonus=2 appears 4.66% (was 4.30%), bonus=3 appears 0.82% (was 0.60%).
		// Updated 2026-05-21 per audit §3 P2; legacy values were inherited
		// from virteon-platform but skew slightly low vs vendor production.
		Bonus2xProbability: 0.0466,
		Bonus3xProbability: 0.0082,

		// Init / response
		SkinVersion:  11,
		VideoQuality: "h50",
		LanguageID:   "es",
		RTP:          0.86,
		BetTypes: []BetType{
			{BettypeID: 1, BettypeName: "Winner", OddsIndexStart: 0, OddsIndexEnd: 5, OddsDecimals: 1},
			{BettypeID: 2, BettypeName: "Forecast in order", OddsIndexStart: 6, OddsIndexEnd: 35, OddsDecimals: 1},
		},

		// Weather
		WeatherOptions:   []string{"fine", "cloudy", "sunny"},
		WeatherWeights:   []float64{0.411, 0.310, 0.279},
		TemperatureRange: IntRange{Min: 10, Max: 26},
		HumidityRange:    IntRange{Min: 60, Max: 80},
		WindSpeedRange:   IntRange{Min: 1, Max: 15},
	}
}

// horseClassicConfig builds the horse_classic (betoffer 241, 7-runner, 4min)
// configuration.
//
// Identity / structure are anchored to verified sources:
//   - internal/config/config.go GAME_TYPES["horse_classic"] (betoffer 241,
//     schedule 102, 7 competitors, 49 odds, interval 240s, EventType "horsec").
//   - internal/wsserver/web_ds_betoffers.go id 241 (RTP 0.85, SkinVersion 10,
//     VideoQuality h50, Winner odds 0-6, Forecast 7-48).
//   - internal/raceutil/raceutil.go epoch 00:01 Malta (already correct; the
//     adapter derives the slot via raceutil, NOT via ScheduleStartTime here).
//
// CALIBRATION STATUS — SMOKE-LEVEL (NOT DS-stat-calibrated, NOT live-money).
// The finish POOL is real DS data (data/videoResults-horse_classic.json,
// 8,256 captured 241 rounds — see data/embed.go SOURCE AUDIT), so the WINNER /
// finish-order distribution tracks the vendor. The ODDS calibration, however,
// is intentionally CONSERVATIVE and structurally minimal:
//   - PositionConstraints / Targets use UNIFORM per-box values (7 boxes ≈
//     14.286 each) — DS horse_classic plays ~uniform (doc 08), and we have no
//     per-sorted-rank WIN-odds marginals for betoffer 241 in Elastic yet (doc
//     08 lists horse_classic only as a "mixed field", no per-box odds table).
//   - RankGap and ForecastRank are DISABLED (no DS tie-rate / exacta-tilt
//     reference for 241). The odds path falls back to the per-position HEAD +
//     flat CombinedFactor — the byte-identical legacy behaviour, same as a
//     dog config with those models off.
//   - OddsFinishCoupling uses the Mallows RIM (UsePL=false) with a modest
//     Theta to give a mild favorite-win bias without claiming a calibrated PL
//     weight vector we do not have.
//
// Per-sorted-rank marginals + RankGap/ForecastRank/PL calibration from Elastic
// are a follow-up (camino "paridad real"); until then horse_classic odds are a
// placeholder and the GLI gate MUST stay closed for betoffer 241 (the
// certifiable surface is videoselector.Select() over the REAL pool, which is
// preserved — but the odds VALUES are not vendor-matched). See the implementer
// report / docs/racegen-design for the known-gap.
//
// PositionConstraints Mean values are a smooth favorite→longshot ramp purely
// so the WIN-odds vector is well-ordered before the overround rescale; they are
// NOT fitted to DS. The overround target derives from RTP 0.85 (1/0.85≈1.1765).
func horseClassicConfig() GameTypeConfigExt {
	return GameTypeConfigExt{
		// Identity
		GameType:   "horse_classic",
		BetofferID: 241,
		ScheduleID: 102,
		EventType:  "horsec",

		// Competitors
		NumberCompetitor: 7,
		NumberWinner:     7,

		// Odds — 7 win + 7*6 forecast = 49.
		NumberOdds:        49,
		WinOddsCount:      7,
		ForecastOddsCount: 42,
		// RTP 0.85 (web_ds_betoffers id 241) ⇒ target overround = 1/0.85 ≈ 1.1765.
		OverroundTarget:    1.1765,
		OverroundTolerance: 0.003,
		// Modest per-race overround spread (placeholder; not DS-fitted for 241).
		OverroundShift:    1.170,
		OverroundLogSigma: 0.30,
		// Modest symmetric shape concentration (placeholder).
		ShapeConcentrationSigma: 0.20,
		MaxOdds:                 17.5,
		// Smooth favorite→longshot ramp for a 7-field. NOT DS-fitted — see the
		// constructor doc. Std/Min/Max give the per-position HEAD path room to
		// vary without crossing ranks; the overround rescale sets the level.
		PositionConstraints: []PositionConstraint{
			{Mean: 4.5, Std: 0.70, Min: 1.8, Max: 7.0},
			{Mean: 5.3, Std: 0.60, Min: 4.0, Max: 9.0},
			{Mean: 6.2, Std: 0.70, Min: 4.4, Max: 10.0},
			{Mean: 7.4, Std: 0.90, Min: 5.0, Max: 12.0},
			{Mean: 9.0, Std: 1.20, Min: 6.0, Max: 14.0},
			{Mean: 11.2, Std: 1.60, Min: 7.0, Max: 17.0},
			{Mean: 13.8, Std: 1.90, Min: 8.0, Max: 17.5},
		},
		// Flat forecast combined factor (placeholder; ForecastRank disabled).
		CombinedFactor: PositionConstraint{Mean: 1.0, Std: 0.0567, Min: 0.85, Max: 1.15},

		// ForecastRank DISABLED — no DS exacta-tilt reference for betoffer 241.
		ForecastRank: ForecastRankFactor{Enabled: false},

		// OddsFinishCoupling: Mallows RIM (no PL weights available for 241).
		// A modest Theta gives a mild favorite-win bias; Theta=0 would be
		// fully uniform. NOT DS-calibrated.
		OddsFinishCoupling: OddsFinishCoupling{
			Theta: 0.15,
			UsePL: false,
		},

		// RankGap DISABLED — no DS within-matrix tie-rate reference for 241.
		RankGap: RankGapModel{Enabled: false},

		// Finish / Videos. VideoPoolPath is BOTH the data.VideoPool key and the
		// URL folder in buildVideoName. The pool IDs ARE the real video file
		// stem — the 7-digit finishing order (e.g. "1237465") served as
		// /.local/horse_classic/1237465.mp4. VideoNameLiteral=true skips the
		// dog-style R%04d reformat. Deploy: symlink the real DSVideo/horse/*.mp4
		// (downloaded by ds-tools/download_horse_for_mac.py) under
		// /.local/horse_classic/ on the VPS so GA stays isolated from DS.
		VideoPoolPath:    "horse_classic",
		VideoFileSuffix:  "", // real files are "1237465.mp4" — no quality suffix
		VideoIDPrefix:    "", // pool IDs are bare 7-digit, no prefix
		VideoNameLiteral: true,
		FinishTimeRange:  Range{Min: 28.54, Max: 30.12},
		GapRange:         Range{Min: 0.01, Max: 0.60},
		GapExponent:      2.7,
		IntervalCount:    2,

		// IPF targets — UNIFORM (7 boxes). DS horse_classic plays ~uniform and
		// we have no per-box DS table for 241. Sums to ≈100. IPF normalizes.
		TargetFirstPlace: map[int]float64{
			1: 14.29, 2: 14.29, 3: 14.29, 4: 14.28, 5: 14.29, 6: 14.28, 7: 14.28,
		},
		TargetSecondPlace: map[int]float64{
			1: 14.29, 2: 14.29, 3: 14.29, 4: 14.28, 5: 14.29, 6: 14.28, 7: 14.28,
		},

		// IPF tuning — legacy defaults (no DS CoV target for 241 to tune to).
		IPFIterations:   50,
		IPFExactaFactor: 0.20,

		// Finish structure
		TimedFinishPositions: 3,
		IntervalSentinel:     false,
		IntervalProportional: false,
		Interval1TimeRange:   Range{Min: 7.50, Max: 7.89},
		Interval2TimeRange:   Range{Min: 23.13, Max: 24.13},
		IntervalRatio1:       0,
		IntervalRatio2:       0,
		IntervalRatioNoise:   0,

		// Timing. RoundIntervalSec = 240 (4min). ScheduleStartTime is the UTC
		// equivalent of the Malta 00:01 epoch (CEST = UTC+2 ⇒ 22:01 the prior
		// day). The adapter derives race numbering via raceutil (already epoch-
		// correct for horse_classic), so this string is informational here.
		RoundIntervalSec:  240,
		VideoDurationSec:  140, // horse_classic mp4 real length (race + result)
		ScheduleStartTime: "22:01:00",

		// Competitor stats (copied from horse-like dog stats; cosmetic).
		WeightRange:      Range{Min: 25.0, Max: 40.0},
		StrikeRateRange:  Range{Min: 10.1, Max: 19.9},
		BestLapRange:     Range{Min: 30.0, Max: 39.9},
		PerformanceRange: Range{Min: 0.31, Max: 0.75},
		NumberOfWinsMax:  5,

		// Bonus — vendor template (web_ds_betoffers id 241: BonusNbr2x 17,
		// BonusNbr3x 3). Use dog-parity empirical rates as placeholder.
		Bonus2xProbability: 0.047,
		Bonus3xProbability: 0.0086,

		// Init / response (anchored to web_ds_betoffers id 241).
		SkinVersion:  10,
		VideoQuality: "h50",
		LanguageID:   "es",
		RTP:          0.85,
		BetTypes: []BetType{
			{BettypeID: 1, BettypeName: "Winner", OddsIndexStart: 0, OddsIndexEnd: 6, OddsDecimals: 1},
			{BettypeID: 2, BettypeName: "Forecast in order", OddsIndexStart: 7, OddsIndexEnd: 48, OddsDecimals: 1},
		},

		// Weather (cosmetic; copied from dog/horse).
		WeatherOptions:   []string{"fine", "cloudy", "sunny"},
		WeatherWeights:   []float64{0.411, 0.310, 0.279},
		TemperatureRange: IntRange{Min: 10, Max: 26},
		HumidityRange:    IntRange{Min: 60, Max: 80},
		WindSpeedRange:   IntRange{Min: 1, Max: 15},
	}
}

// ----------------------------------------------------------------------------
// Registry
// ----------------------------------------------------------------------------

// supportedOrder is the canonical ordering returned by SupportedGameTypes.
var supportedOrder = []string{"dog8", "dog6", "horse_classic"}

var registry = map[string]GameTypeConfigExt{
	"dog8":          dog8Config(),
	"dog6":          dog6Config(),
	"horse_classic": horseClassicConfig(),
}

// init validates every registry entry. A binary with bad calibration
// cannot start — we'd rather panic at boot than emit corrupt races.
func init() {
	for gt, cfg := range registry {
		if err := cfg.Validate(); err != nil {
			panic(fmt.Sprintf("config: registry entry %q failed validation: %v", gt, err))
		}
	}
}

// Get returns the extended config for gameType, or a non-nil error if
// the game type is not registered.
func Get(gameType string) (GameTypeConfigExt, error) {
	c, ok := registry[gameType]
	if !ok {
		return GameTypeConfigExt{}, fmt.Errorf("config: unknown game type %q (supported: %v)",
			gameType, supportedOrder)
	}
	return c, nil
}

// SupportedGameTypes returns the canonical list of registered game types
// in declaration order. Stable across calls — safe to range over.
func SupportedGameTypes() []string {
	out := make([]string, len(supportedOrder))
	copy(out, supportedOrder)
	return out
}

// ----------------------------------------------------------------------------
// Validation
// ----------------------------------------------------------------------------

// Validate enforces structural invariants on the config. It runs at
// package init for every registered entry. Returns the first error
// found; callers should treat any non-nil return as fatal.
func (c *GameTypeConfigExt) Validate() error {
	if c.NumberCompetitor <= 0 {
		return fmt.Errorf("NumberCompetitor must be > 0, got %d", c.NumberCompetitor)
	}

	// Position constraints — one per competitor.
	if len(c.PositionConstraints) != c.NumberCompetitor {
		return fmt.Errorf("len(PositionConstraints)=%d, want %d",
			len(c.PositionConstraints), c.NumberCompetitor)
	}

	// Weather options ↔ weights must align, weights sum ≈ 1.
	if len(c.WeatherWeights) != len(c.WeatherOptions) {
		return fmt.Errorf("len(WeatherWeights)=%d != len(WeatherOptions)=%d",
			len(c.WeatherWeights), len(c.WeatherOptions))
	}
	var wsum float64
	for _, w := range c.WeatherWeights {
		if w < 0 {
			return fmt.Errorf("WeatherWeights contains negative entry %g", w)
		}
		wsum += w
	}
	if wsum < 0.999 || wsum > 1.001 {
		return fmt.Errorf("WeatherWeights sum=%g, want ∈ [0.999, 1.001]", wsum)
	}

	// IPF target maps must have one entry per runner. Entries must be
	// positive (the algorithm normalizes, so absolute magnitude is not
	// constrained — see TargetFirstPlace doc comment).
	if err := validateIPFTargets("TargetFirstPlace", c.TargetFirstPlace, c.NumberCompetitor); err != nil {
		return err
	}
	if err := validateIPFTargets("TargetSecondPlace", c.TargetSecondPlace, c.NumberCompetitor); err != nil {
		return err
	}

	// BetType odds-index ranges must contiguously cover [0, NumberOdds-1].
	if err := validateBetTypeCoverage(c.BetTypes, c.NumberOdds); err != nil {
		return err
	}

	// Odds↔finish coupling dispersion must be non-negative (0 = uniform).
	if c.OddsFinishCoupling.Theta < 0 {
		return fmt.Errorf("OddsFinishCoupling.Theta=%g, want >= 0", c.OddsFinishCoupling.Theta)
	}

	// Rank-gap model — only validated when enabled (no-op otherwise). Pass
	// WinOddsCount (the count the rank-space loop actually iterates), not
	// NumberCompetitor, so the slice-length guard matches the runtime indexing.
	if c.RankGap.Enabled {
		if err := c.RankGap.validate(c.WinOddsCount, c.MaxOdds); err != nil {
			return err
		}
	}

	// Forecast rank-aware factor — only validated when enabled. Pass
	// WinOddsCount (the rank vector spans the n WIN runners) and the global
	// target (CombinedFactor.Mean) the per-rank means must average to.
	if c.ForecastRank.Enabled {
		if err := c.ForecastRank.validate(c.WinOddsCount, c.CombinedFactor.Mean); err != nil {
			return err
		}
	}

	// Plackett-Luce odds↔finish coupling — only validated when enabled.
	if err := c.OddsFinishCoupling.validate(c.WinOddsCount); err != nil {
		return err
	}

	return nil
}

// validate enforces the rank-gap model's structural invariants. n is the
// WIN-odds count (gaps span n-1 adjacent pairs); maxOdds is the config's hard
// odds clamp.
func (m *RankGapModel) validate(n int, maxOdds float64) error {
	want := n - 1
	if len(m.GapMean) != want || len(m.GapStd) != want ||
		len(m.GapMin) != want || len(m.GapMax) != want {
		return fmt.Errorf("RankGap: gap slices must each have length n-1=%d (got %d/%d/%d/%d)",
			want, len(m.GapMean), len(m.GapStd), len(m.GapMin), len(m.GapMax))
	}
	for r := 0; r < want; r++ {
		if m.GapStd[r] < 0 {
			return fmt.Errorf("RankGap: GapStd[%d]=%g, want >= 0", r, m.GapStd[r])
		}
		if m.GapMin[r] > m.GapMax[r] {
			return fmt.Errorf("RankGap: GapMin[%d]=%g > GapMax[%d]=%g", r, m.GapMin[r], r, m.GapMax[r])
		}
		// GapMax<0 would fold every gap to 0 (all ranks collapse onto the
		// favorite anchor) — almost certainly a config error.
		if m.GapMax[r] < 0 {
			return fmt.Errorf("RankGap: GapMax[%d]=%g, want >= 0", r, m.GapMax[r])
		}
	}
	if m.OddsFloor <= 0 {
		return fmt.Errorf("RankGap: OddsFloor=%g, want > 0", m.OddsFloor)
	}
	if m.LongshotMaxOdds <= 0 || m.LongshotMaxOdds > maxOdds {
		return fmt.Errorf("RankGap: LongshotMaxOdds=%g, want ∈ (0, MaxOdds=%g]", m.LongshotMaxOdds, maxOdds)
	}
	// softCapOdds runs AFTER the OddsFloor clamp, so a soft cap below the floor
	// would silently pull floored values back under it.
	if m.LongshotMaxOdds < m.OddsFloor {
		return fmt.Errorf("RankGap: LongshotMaxOdds=%g < OddsFloor=%g", m.LongshotMaxOdds, m.OddsFloor)
	}
	return nil
}

// validate enforces the forecast rank-factor invariants. n is the WIN-odds
// count (one mean per rank); globalMean is the CombinedFactor.Mean the per-rank
// means must average to so the per-slot forecast parity is preserved.
func (f *ForecastRankFactor) validate(n int, globalMean float64) error {
	if len(f.Mean) != n {
		return fmt.Errorf("ForecastRank: Mean must have length n=%d (got %d)", n, len(f.Mean))
	}
	if f.Std < 0 {
		return fmt.Errorf("ForecastRank: Std=%g, want >= 0", f.Std)
	}
	if f.Min > f.Max {
		return fmt.Errorf("ForecastRank: Min=%g > Max=%g", f.Min, f.Max)
	}
	// A factor <= 0 would yield non-positive forecast odds.
	if f.Min <= 0 {
		return fmt.Errorf("ForecastRank: Min=%g, want > 0", f.Min)
	}
	// Global-mean preservation: the arithmetic mean of the per-rank means must
	// equal CombinedFactor.Mean (each rank leads exactly n-1 of the n(n-1)
	// pairs). Drift here would tip the per-slot forecast parity gate.
	var sum float64
	for _, m := range f.Mean {
		sum += m
	}
	if got := sum / float64(n); math.Abs(got-globalMean) > 1e-3 {
		return fmt.Errorf("ForecastRank: mean(Mean)=%.6f, want CombinedFactor.Mean=%.6f (±1e-3)", got, globalMean)
	}
	return nil
}

// validateIPFTargets checks that an IPF target map has exactly one entry
// per runner (keys 1..n), every value is positive, and the sum is
// strictly positive. The sum is NOT pinned to a specific range — see
// TargetFirstPlace doc comment for rationale.
func validateIPFTargets(field string, m map[int]float64, n int) error {
	if len(m) != n {
		return fmt.Errorf("len(%s)=%d, want %d", field, len(m), n)
	}
	var sum float64
	for i := 1; i <= n; i++ {
		v, ok := m[i]
		if !ok {
			return fmt.Errorf("%s missing key %d", field, i)
		}
		if v <= 0 {
			return fmt.Errorf("%s[%d]=%g, want > 0", field, i, v)
		}
		sum += v
	}
	if sum <= 0 {
		return fmt.Errorf("%s sum=%g, want > 0", field, sum)
	}
	return nil
}

// validateBetTypeCoverage checks that BetTypes' OddsIndex ranges form a
// contiguous, non-overlapping cover of [0, numberOdds-1]. Order matters
// — slots are assumed to be declared in ascending order.
func validateBetTypeCoverage(bts []BetType, numberOdds int) error {
	if len(bts) == 0 {
		return fmt.Errorf("BetTypes is empty")
	}
	expected := 0
	for i, bt := range bts {
		if bt.OddsIndexStart != expected {
			return fmt.Errorf("BetTypes[%d] (%s): OddsIndexStart=%d, want %d (must be contiguous from previous slice)",
				i, bt.BettypeName, bt.OddsIndexStart, expected)
		}
		if bt.OddsIndexEnd < bt.OddsIndexStart {
			return fmt.Errorf("BetTypes[%d] (%s): OddsIndexEnd=%d < OddsIndexStart=%d",
				i, bt.BettypeName, bt.OddsIndexEnd, bt.OddsIndexStart)
		}
		expected = bt.OddsIndexEnd + 1
	}
	if expected != numberOdds {
		return fmt.Errorf("BetTypes cover [0, %d), want [0, %d)", expected, numberOdds)
	}
	return nil
}
