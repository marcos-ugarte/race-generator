package generators

import (
	"regexp"
	"strings"
	"testing"
)

// validWind matches "<int> <16pt-dir>". Note that NNE/NNW etc. must be
// permitted, hence the explicit alternation rather than a class.
var windRE = regexp.MustCompile(`^[0-9]+ (N|NNE|NE|ENE|E|ESE|SE|SSE|S|SSW|SW|WSW|W|WNW|NW|NNW)$`)

func TestConditionsShape(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	got := GenerateConditions(mustMT(t), cfg)

	if got.CourseConditions != "fast" {
		t.Errorf("CourseConditions=%q, want \"fast\"", got.CourseConditions)
	}
	found := false
	for _, opt := range cfg.WeatherOptions {
		if got.Weather == opt {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Weather=%q not in %v", got.Weather, cfg.WeatherOptions)
	}
	if !windRE.MatchString(got.Wind) {
		t.Errorf("Wind=%q does not match `<speed> <16pt-dir>`", got.Wind)
	}
	// Wind speed within configured range
	parts := strings.SplitN(got.Wind, " ", 2)
	var speed int
	if _, err := scanInt(parts[0], &speed); err != nil {
		t.Fatalf("parse wind speed %q: %v", parts[0], err)
	}
	if speed < cfg.WindSpeedRange.Min || speed > cfg.WindSpeedRange.Max {
		t.Errorf("wind speed %d outside %+v", speed, cfg.WindSpeedRange)
	}
	if got.Temperature < cfg.TemperatureRange.Min || got.Temperature > cfg.TemperatureRange.Max {
		t.Errorf("Temperature %d outside %+v", got.Temperature, cfg.TemperatureRange)
	}
	if got.Humidity < cfg.HumidityRange.Min || got.Humidity > cfg.HumidityRange.Max {
		t.Errorf("Humidity %d outside %+v", got.Humidity, cfg.HumidityRange)
	}
}

// scanInt is a tiny strconv shim to keep the regex test self-contained.
func scanInt(s string, dst *int) (int, error) {
	v := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return i, &strconvErr{s}
		}
		v = v*10 + int(s[i]-'0')
	}
	*dst = v
	return len(s), nil
}

type strconvErr struct{ s string }

func (e *strconvErr) Error() string { return "not an int: " + e.s }

func TestConditionsWeatherWeighted(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	// fresh seed: 10000 independent samples — we still want determinism,
	// so use the package test seed but draw 10000 in a single MT instance.
	mt := mustMT(t)
	const trials = 10000
	counts := make(map[string]int, len(cfg.WeatherOptions))
	for i := 0; i < trials; i++ {
		c := GenerateConditions(mt, cfg)
		counts[c.Weather]++
	}
	const tol = 0.02 // 2 percentage points
	for i, opt := range cfg.WeatherOptions {
		expected := cfg.WeatherWeights[i]
		freq := float64(counts[opt]) / float64(trials)
		if diff := freq - expected; diff < -tol || diff > tol {
			t.Errorf("weather %q: freq %.4f, want %.4f ± %.2f (delta=%.4f)",
				opt, freq, expected, tol, diff)
		}
	}
}

func TestConditionsDeterministic(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	a := GenerateConditions(mustMT(t), cfg)
	b := GenerateConditions(mustMT(t), cfg)
	if a != b {
		t.Fatalf("non-deterministic conditions: a=%+v b=%+v", a, b)
	}
}
