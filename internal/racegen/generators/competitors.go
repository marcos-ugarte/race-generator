// Package generators implements the per-race generators (Phase 3 of the
// racegen port). Each file mirrors one of the legacy TypeScript modules
// at virteon-platform/packages/game-engine/src/generators/.
//
// All randomness flows through the rng package — no math/rand, no time.Now()
// fallbacks. Generators consume an externally-seeded *rng.MT19937 so the
// orchestrator can replay any race byte-for-byte from its seed.
package generators

import (
	"strconv"

	"vg-racegen/internal/racegen/config"
	"vg-racegen/internal/racegen/data"
	"vg-racegen/internal/racegen/rng"
)

// Competitor is the canonical per-runner statistics block emitted for each
// race. Field tags match the JSON shape consumed by the legacy adapter and
// downstream Webview clients (see virteon-platform/packages/game-engine/src/types.ts).
//
// Performance is encoded with 6 decimals; all other floats use fewer.
type Competitor struct {
	Name              string  `json:"name"`
	Weight            float64 `json:"weight"`
	NumberOfRaces     int     `json:"numberOfRaces"`
	NumberOfWins      int     `json:"numberOfWins"`
	NumberOfSecond    int     `json:"numberOfSecond"`
	StrikeRate        float64 `json:"strikeRate"`
	BestLap           float64 `json:"bestLap"`
	Performance       float64 `json:"performance"`
	ResultHistory     string  `json:"resultHistory"`
	Last5             string  `json:"last5"`
	Nbr1              int     `json:"nbr1"`
	Nbr2              int     `json:"nbr2"`
	Nbr3              int     `json:"nbr3"`
	RacesForStatistic int     `json:"racesForStatistic"`
	Trend             int     `json:"trend"`
}

// GenerateCompetitors produces a map keyed by 1-based string position
// ("1".."N") containing N competitor stat blocks for one race.
//
// Name selection follows the legacy cooldown rule
// (competitors.ts:62-83): start from the global dog-name pool minus
// excludeNames; if fewer than 2*NumberCompetitor names remain, fall
// back to the full pool. Within a single race we apply rejection
// sampling against a usedNames set to guarantee within-race uniqueness.
// excludeNames may be nil.
func GenerateCompetitors(mt rng.Source, cfg config.GameTypeConfigExt, excludeNames map[string]bool) map[string]Competitor {
	allNames := data.DogNames()
	n := cfg.NumberCompetitor

	// Build availablePool = allNames \ excludeNames. If too small, fall back.
	availablePool := allNames
	if len(excludeNames) > 0 {
		filtered := make([]string, 0, len(allNames))
		for _, nm := range allNames {
			if !excludeNames[nm] {
				filtered = append(filtered, nm)
			}
		}
		if len(filtered) >= 2*n {
			availablePool = filtered
		}
	}

	out := make(map[string]Competitor, n)
	usedNames := make(map[string]struct{}, n)

	for i := 1; i <= n; i++ {
		c := generateCompetitor(mt, cfg)

		// Rejection-sample a unique name from availablePool.
		name := availablePool[rng.CertifiedInt(mt, 0, len(availablePool)-1)]
		for {
			if _, dup := usedNames[name]; !dup {
				break
			}
			name = availablePool[rng.CertifiedInt(mt, 0, len(availablePool)-1)]
		}
		c.Name = name
		usedNames[name] = struct{}{}

		out[strconv.Itoa(i)] = c
	}
	return out
}

// generateCompetitor builds one Competitor with stats only — the caller
// overwrites Name. Ranges are taken from cfg; the hard-coded ranges
// (NumberOfSecond, NumberOfRaces, Nbr1, Nbr2, Nbr3, Trend) match the
// legacy TS (competitors.ts:23-43).
func generateCompetitor(mt rng.Source, cfg config.GameTypeConfigExt) Competitor {
	return Competitor{
		Weight:            rng.CertifiedFloatRange(mt, cfg.WeightRange.Min, cfg.WeightRange.Max, 1),
		NumberOfRaces:     rng.CertifiedInt(mt, 0, 9),
		NumberOfWins:      rng.CertifiedInt(mt, 0, cfg.NumberOfWinsMax),
		NumberOfSecond:    rng.CertifiedInt(mt, 0, 6),
		StrikeRate:        rng.CertifiedFloatRange(mt, cfg.StrikeRateRange.Min, cfg.StrikeRateRange.Max, 2),
		BestLap:           rng.CertifiedFloatRange(mt, cfg.BestLapRange.Min, cfg.BestLapRange.Max, 2),
		Performance:       rng.CertifiedFloatRange(mt, cfg.PerformanceRange.Min, cfg.PerformanceRange.Max, 6),
		ResultHistory:     generateLast5(mt, ";", cfg.NumberCompetitor),
		Last5:             generateLast5(mt, "|", cfg.NumberCompetitor),
		Nbr1:              rng.CertifiedInt(mt, 0, 6),
		Nbr2:              rng.CertifiedInt(mt, 0, 7),
		Nbr3:              rng.CertifiedInt(mt, 0, 6),
		RacesForStatistic: 20,
		Trend:             rng.CertifiedInt(mt, 1, 5),
	}
}

// generateLast5 builds a 5-element separator-joined history string of
// finishing positions in [1, maxPosition]. Matches competitors.ts:15-20.
func generateLast5(mt rng.Source, sep string, maxPosition int) string {
	// Manual join — avoid pulling strings.Join + intermediate slice
	// allocations on the per-race hot path.
	var b []byte
	for i := 0; i < 5; i++ {
		v := rng.CertifiedInt(mt, 1, maxPosition)
		if i > 0 {
			b = append(b, sep...)
		}
		b = strconv.AppendInt(b, int64(v), 10)
	}
	return string(b)
}
