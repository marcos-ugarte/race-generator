package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestGetDog8(t *testing.T) {
	c, err := Get("dog8")
	if err != nil {
		t.Fatalf("Get(dog8) error: %v", err)
	}
	if c.BetofferID != 541 {
		t.Errorf("BetofferID: got %d, want 541", c.BetofferID)
	}
	if c.NumberCompetitor != 8 {
		t.Errorf("NumberCompetitor: got %d, want 8", c.NumberCompetitor)
	}
	// Recalibrated 2026-05-21 from 1.1723 to 1.1459 (vendor measured RTP 87.27%).
	// See test/parity_odds/results/full_audit_2026-05-20.md §2 P3.
	if c.OverroundTarget != 1.1459 {
		t.Errorf("OverroundTarget: got %g, want 1.1459", c.OverroundTarget)
	}
	if len(c.PositionConstraints) != 8 {
		t.Errorf("len(PositionConstraints): got %d, want 8", len(c.PositionConstraints))
	}
	// Re-baselined 2026-05-31 (DS odds-shape parity): longshot cap nudged
	// 17.1→17.5 to match the DS rank-8 max. See doc 08 §4.6.
	if c.MaxOdds != 17.5 {
		t.Errorf("MaxOdds: got %g, want 17.5", c.MaxOdds)
	}
}

func TestGetDog6(t *testing.T) {
	c, err := Get("dog6")
	if err != nil {
		t.Fatalf("Get(dog6) error: %v", err)
	}
	if c.BetofferID != 141 {
		t.Errorf("BetofferID: got %d, want 141", c.BetofferID)
	}
	if c.NumberCompetitor != 6 {
		t.Errorf("NumberCompetitor: got %d, want 6", c.NumberCompetitor)
	}
	if c.MaxOdds != 12.9 {
		t.Errorf("MaxOdds: got %g, want 12.9", c.MaxOdds)
	}
	if len(c.PositionConstraints) != 6 {
		t.Errorf("len(PositionConstraints): got %d, want 6", len(c.PositionConstraints))
	}
}

func TestGetHorseClassic(t *testing.T) {
	c, err := Get("horse_classic")
	if err != nil {
		t.Fatalf("Get(horse_classic) error: %v", err)
	}
	if c.BetofferID != 241 {
		t.Errorf("BetofferID: got %d, want 241", c.BetofferID)
	}
	if c.ScheduleID != 102 {
		t.Errorf("ScheduleID: got %d, want 102", c.ScheduleID)
	}
	if c.EventType != "horsec" {
		t.Errorf("EventType: got %q, want %q", c.EventType, "horsec")
	}
	if c.NumberCompetitor != 7 {
		t.Errorf("NumberCompetitor: got %d, want 7", c.NumberCompetitor)
	}
	if c.NumberOdds != 49 {
		t.Errorf("NumberOdds: got %d, want 49", c.NumberOdds)
	}
	if c.WinOddsCount != 7 {
		t.Errorf("WinOddsCount: got %d, want 7", c.WinOddsCount)
	}
	if c.ForecastOddsCount != 42 {
		t.Errorf("ForecastOddsCount: got %d, want 42", c.ForecastOddsCount)
	}
	if len(c.PositionConstraints) != 7 {
		t.Errorf("len(PositionConstraints): got %d, want 7", len(c.PositionConstraints))
	}
	if c.RoundIntervalSec != 240 {
		t.Errorf("RoundIntervalSec: got %d, want 240", c.RoundIntervalSec)
	}
	if c.VideoPoolPath != "horse_classic" {
		t.Errorf("VideoPoolPath: got %q, want %q", c.VideoPoolPath, "horse_classic")
	}
	// BetTypes must contiguously cover [0,48]: Winner 0-6, Forecast 7-48.
	if err := c.Validate(); err != nil {
		t.Errorf("Validate(horse_classic): %v", err)
	}
	if len(c.BetTypes) != 2 ||
		c.BetTypes[0].OddsIndexStart != 0 || c.BetTypes[0].OddsIndexEnd != 6 ||
		c.BetTypes[1].OddsIndexStart != 7 || c.BetTypes[1].OddsIndexEnd != 48 {
		t.Errorf("BetTypes coverage unexpected: %+v", c.BetTypes)
	}
}

func TestGetUnsupported(t *testing.T) {
	for _, gt := range []string{"horse7", "dog63", "", "DOG8"} {
		if _, err := Get(gt); err == nil {
			t.Errorf("Get(%q): want non-nil error, got nil", gt)
		} else if !strings.Contains(err.Error(), "unknown game type") {
			t.Errorf("Get(%q): error %q missing 'unknown game type'", gt, err)
		}
	}
}

func TestSupportedGameTypes(t *testing.T) {
	got := SupportedGameTypes()
	want := []string{"dog8", "dog6", "horse_classic"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SupportedGameTypes: got %v, want %v", got, want)
	}

	// Returned slice must be a copy — mutation should not affect future calls.
	got[0] = "MUTATED"
	again := SupportedGameTypes()
	if again[0] != "dog8" {
		t.Errorf("SupportedGameTypes leaked internal slice: %v", again)
	}
}

func TestValidateAllConfigs(t *testing.T) {
	for _, gt := range SupportedGameTypes() {
		c, err := Get(gt)
		if err != nil {
			t.Fatalf("Get(%s) error: %v", gt, err)
		}
		if err := c.Validate(); err != nil {
			t.Errorf("Validate(%s) failed: %v", gt, err)
		}
	}
}

// TestForecastRankMeanPreserved guards the invariant that makes the rank-aware
// forecast factor safe for the per-slot parity gate: the arithmetic mean of the
// per-rank means must equal CombinedFactor.Mean (each rank leads exactly n-1 of
// the n(n-1) ordered pairs, so the global factor is the simple mean). If a
// future calibration edit drifts this, parity_odds would silently shift.
func TestForecastRankMeanPreserved(t *testing.T) {
	for _, gt := range SupportedGameTypes() {
		c, err := Get(gt)
		if err != nil {
			t.Fatalf("Get(%s) error: %v", gt, err)
		}
		if !c.ForecastRank.Enabled {
			continue
		}
		var sum float64
		for _, m := range c.ForecastRank.Mean {
			sum += m
		}
		got := sum / float64(len(c.ForecastRank.Mean))
		if diff := got - c.CombinedFactor.Mean; diff > 1e-3 || diff < -1e-3 {
			t.Errorf("%s: mean(ForecastRank.Mean)=%.6f, want CombinedFactor.Mean=%.6f", gt, got, c.CombinedFactor.Mean)
		}
	}
}
