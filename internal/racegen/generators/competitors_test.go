package generators

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"vg-racegen/internal/racegen/config"
	"vg-racegen/internal/racegen/rng"
)

// seed used for deterministic tests across the package
const testSeedHex = "4242424242424242424242424242424242424242424242424242424242424242"

func mustMT(t *testing.T) *rng.MT19937 {
	t.Helper()
	mt, err := rng.NewMT19937WithSeedHex(testSeedHex)
	if err != nil {
		t.Fatalf("seed mt: %v", err)
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

func TestCompetitorsCount_dog8(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	got := GenerateCompetitors(mustMT(t), cfg, nil)
	if len(got) != 8 {
		t.Fatalf("len(competitors)=%d, want 8", len(got))
	}
	for i := 1; i <= 8; i++ {
		if _, ok := got[strconv.Itoa(i)]; !ok {
			t.Errorf("missing key %d", i)
		}
	}
}

func TestCompetitorsCount_dog6(t *testing.T) {
	cfg := mustConfig(t, "dog6")
	got := GenerateCompetitors(mustMT(t), cfg, nil)
	if len(got) != 6 {
		t.Fatalf("len(competitors)=%d, want 6", len(got))
	}
}

func TestCompetitorsUniqueNames(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	got := GenerateCompetitors(mustMT(t), cfg, nil)
	seen := make(map[string]struct{}, len(got))
	for k, c := range got {
		if c.Name == "" {
			t.Errorf("entry %s has empty Name", k)
		}
		if _, dup := seen[c.Name]; dup {
			t.Errorf("duplicate name %q at key %s", c.Name, k)
		}
		seen[c.Name] = struct{}{}
	}
}

func TestCompetitorsRespectsCooldown(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	// Run an unconstrained pass to collect a few representative names
	probe := GenerateCompetitors(mustMT(t), cfg, nil)
	excludeNames := make(map[string]bool, 5)
	i := 0
	for _, c := range probe {
		excludeNames[c.Name] = true
		i++
		if i == 5 {
			break
		}
	}
	if len(excludeNames) != 5 {
		t.Fatalf("expected 5 excluded names, got %d", len(excludeNames))
	}

	// With only 5 names excluded out of 1424, the cooldown filter must
	// be honoured (|pool|-5 >> 2*8 fallback threshold).
	got := GenerateCompetitors(mustMT(t), cfg, excludeNames)
	for _, c := range got {
		if excludeNames[c.Name] {
			t.Errorf("excluded name %q reappeared (cooldown not honoured)", c.Name)
		}
	}
}

func TestCompetitorsDeterministic(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	a := GenerateCompetitors(mustMT(t), cfg, nil)
	b := GenerateCompetitors(mustMT(t), cfg, nil)
	ja, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal a: %v", err)
	}
	jb, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal b: %v", err)
	}
	if string(ja) != string(jb) {
		t.Fatalf("non-deterministic competitors:\n a=%s\n b=%s", ja, jb)
	}
}

func TestCompetitorsStatsInRange(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	got := GenerateCompetitors(mustMT(t), cfg, nil)
	for k, c := range got {
		if c.Weight < cfg.WeightRange.Min || c.Weight > cfg.WeightRange.Max {
			t.Errorf("%s: weight %v outside %+v", k, c.Weight, cfg.WeightRange)
		}
		if c.StrikeRate < cfg.StrikeRateRange.Min || c.StrikeRate > cfg.StrikeRateRange.Max {
			t.Errorf("%s: strikeRate %v outside %+v", k, c.StrikeRate, cfg.StrikeRateRange)
		}
		if c.BestLap < cfg.BestLapRange.Min || c.BestLap > cfg.BestLapRange.Max {
			t.Errorf("%s: bestLap %v outside %+v", k, c.BestLap, cfg.BestLapRange)
		}
		if c.Performance < cfg.PerformanceRange.Min || c.Performance > cfg.PerformanceRange.Max {
			t.Errorf("%s: performance %v outside %+v", k, c.Performance, cfg.PerformanceRange)
		}
		if c.NumberOfRaces < 0 || c.NumberOfRaces > 9 {
			t.Errorf("%s: numberOfRaces %d outside [0,9]", k, c.NumberOfRaces)
		}
		if c.NumberOfWins < 0 || c.NumberOfWins > cfg.NumberOfWinsMax {
			t.Errorf("%s: numberOfWins %d outside [0,%d]", k, c.NumberOfWins, cfg.NumberOfWinsMax)
		}
		if c.NumberOfSecond < 0 || c.NumberOfSecond > 6 {
			t.Errorf("%s: numberOfSecond %d outside [0,6]", k, c.NumberOfSecond)
		}
		if c.RacesForStatistic != 20 {
			t.Errorf("%s: racesForStatistic=%d, want 20", k, c.RacesForStatistic)
		}
		if c.Trend < 1 || c.Trend > 5 {
			t.Errorf("%s: trend %d outside [1,5]", k, c.Trend)
		}
		if c.Nbr1 < 0 || c.Nbr1 > 6 {
			t.Errorf("%s: nbr1 %d outside [0,6]", k, c.Nbr1)
		}
		if c.Nbr2 < 0 || c.Nbr2 > 7 {
			t.Errorf("%s: nbr2 %d outside [0,7]", k, c.Nbr2)
		}
		if c.Nbr3 < 0 || c.Nbr3 > 6 {
			t.Errorf("%s: nbr3 %d outside [0,6]", k, c.Nbr3)
		}
		// Result history shape sanity
		if parts := strings.Split(c.ResultHistory, ";"); len(parts) != 5 {
			t.Errorf("%s: resultHistory=%q want 5 parts", k, c.ResultHistory)
		}
		if parts := strings.Split(c.Last5, "|"); len(parts) != 5 {
			t.Errorf("%s: last5=%q want 5 parts", k, c.Last5)
		}
	}
}
