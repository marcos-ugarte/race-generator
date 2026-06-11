package adapter

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"vg-racegen/internal/racegen/config"
	"vg-racegen/internal/racegen/data"
	"vg-racegen/internal/racegen/generators"
	"vg-racegen/internal/racegen/rng"
	"vg-racegen/internal/racegen/videoselector"
)

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

func mustSelector(t *testing.T, gt string) *videoselector.Selector {
	t.Helper()
	cfg := mustConfig(t, gt)
	pool := data.VideoPool(gt)
	if pool == nil {
		t.Fatalf("nil pool for %s", gt)
	}
	sel, err := videoselector.New(pool, cfg)
	if err != nil {
		t.Fatalf("videoselector.New: %v", err)
	}
	return sel
}

func fixedSlot() time.Time {
	return time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
}

func mustGame(t *testing.T, gt string) (generators.Game, config.GameTypeConfigExt) {
	t.Helper()
	cfg := mustConfig(t, gt)
	sel := mustSelector(t, gt)
	jp := &generators.JackpotState{Current: 45000.0}
	g, err := generators.GenerateGame(mustMT(t), cfg, sel, jp, fixedSlot(), nil, nil)
	if err != nil {
		t.Fatalf("GenerateGame(%s): %v", gt, err)
	}
	return g, cfg
}

func TestToGameRoundDog8(t *testing.T) {
	g, cfg := mustGame(t, "dog8")
	round, results, err := ToGameRound(g, cfg)
	if err != nil {
		t.Fatalf("ToGameRound: %v", err)
	}
	if round.RoundCode != g.RoundCode {
		t.Errorf("RoundCode=%q, want %q", round.RoundCode, g.RoundCode)
	}
	if round.GameTypeID != cfg.BetofferID {
		t.Errorf("GameTypeID=%d, want %d", round.GameTypeID, cfg.BetofferID)
	}
	if round.GameType != "dog8" {
		t.Errorf("GameType=%q, want dog8", round.GameType)
	}
	if round.RaceNumber != strconv.Itoa(g.IDRace) {
		t.Errorf("RaceNumber=%q, want %q", round.RaceNumber, strconv.Itoa(g.IDRace))
	}
	if round.RaceDate != "2026-05-17" {
		t.Errorf("RaceDate=%q, want 2026-05-17", round.RaceDate)
	}
	if round.Status != "F" {
		t.Errorf("Status=%q, want F", round.Status)
	}
	if round.CompetitorsCount != 8 {
		t.Errorf("CompetitorsCount=%d, want 8", round.CompetitorsCount)
	}
	if round.Bonus != g.Bonus {
		t.Errorf("Bonus=%d, want %d", round.Bonus, g.Bonus)
	}
	if round.VideoName != g.Finish.VideoName.MP4 {
		t.Errorf("VideoName=%q, want %q", round.VideoName, g.Finish.VideoName.MP4)
	}
	if round.Weather != g.Conditions.Weather {
		t.Errorf("Weather=%q, want %q", round.Weather, g.Conditions.Weather)
	}
	if round.Temperature != g.Conditions.Temperature {
		t.Errorf("Temperature=%d, want %d", round.Temperature, g.Conditions.Temperature)
	}
	if round.Humidity != g.Conditions.Humidity {
		t.Errorf("Humidity=%d, want %d", round.Humidity, g.Conditions.Humidity)
	}
	if round.Wind != g.Conditions.Wind {
		t.Errorf("Wind=%q, want %q", round.Wind, g.Conditions.Wind)
	}
	if round.CourseConditions != g.Conditions.CourseConditions {
		t.Errorf("CourseConditions=%q, want %q", round.CourseConditions, g.Conditions.CourseConditions)
	}
	if round.RoundInterval != g.RoundInterval {
		t.Errorf("RoundInterval=%d, want %d", round.RoundInterval, g.RoundInterval)
	}
	wantStart := "2026-05-17 12:00:00"
	if round.VideoStartDt != wantStart {
		t.Errorf("VideoStartDt=%q, want %q", round.VideoStartDt, wantStart)
	}
	wantEnd := "2026-05-17 12:01:00" // videoStart + 60s = real dog8 mp4 length (race+result); 240s is start-to-start slot spacing
	if round.VideoEndDt != wantEnd {
		t.Errorf("VideoEndDt=%q, want %q", round.VideoEndDt, wantEnd)
	}
	if round.ScheduledAt != round.VideoStartDt {
		t.Errorf("ScheduledAt=%q, want VideoStartDt=%q", round.ScheduledAt, round.VideoStartDt)
	}
	if round.CreatedAt != round.VideoStartDt {
		t.Errorf("CreatedAt=%q, want VideoStartDt=%q (NOT time.Now)", round.CreatedAt, round.VideoStartDt)
	}
	if round.FinishedAt == nil {
		t.Fatalf("FinishedAt is nil")
	}
	if *round.FinishedAt != wantEnd {
		t.Errorf("*FinishedAt=%q, want %q", *round.FinishedAt, wantEnd)
	}

	// Results: ordered 1..8, all present, every CompetitorIndex matches
	// what's in g.Finish.Finish.
	if len(results) != 8 {
		t.Fatalf("len(results)=%d, want 8", len(results))
	}
	for i, r := range results {
		wantPos := i + 1
		if r.Position != wantPos {
			t.Errorf("results[%d].Position=%d, want %d", i, r.Position, wantPos)
		}
		if r.GameRoundID != g.RoundCode {
			t.Errorf("results[%d].GameRoundID=%q, want %q", i, r.GameRoundID, g.RoundCode)
		}
		fp := g.Finish.Finish[strconv.Itoa(wantPos)]
		if r.RunnerNumber != fp.CompetitorIndex {
			t.Errorf("results[%d].RunnerNumber=%d, want %d", i, r.RunnerNumber, fp.CompetitorIndex)
		}
		// FinishTime pointer equality at the source value is sufficient.
		if (r.FinishTime == nil) != (fp.Time == nil) {
			t.Errorf("results[%d].FinishTime nil mismatch: got=%v want=%v", i, r.FinishTime, fp.Time)
		}
		if r.FinishTime != nil && fp.Time != nil && *r.FinishTime != *fp.Time {
			t.Errorf("results[%d].FinishTime=%v, want %v", i, *r.FinishTime, *fp.Time)
		}
	}
}

func TestToGameRoundDog6(t *testing.T) {
	g, cfg := mustGame(t, "dog6")
	round, results, err := ToGameRound(g, cfg)
	if err != nil {
		t.Fatalf("ToGameRound: %v", err)
	}
	if round.GameType != "dog6" {
		t.Errorf("GameType=%q, want dog6", round.GameType)
	}
	if round.CompetitorsCount != 6 {
		t.Errorf("CompetitorsCount=%d, want 6", round.CompetitorsCount)
	}
	if len(results) != 6 {
		t.Fatalf("len(results)=%d, want 6", len(results))
	}
	for i, r := range results {
		if r.Position != i+1 {
			t.Errorf("results[%d].Position=%d, want %d", i, r.Position, i+1)
		}
	}
}

func TestCompetitorsJSONShape(t *testing.T) {
	for _, gt := range []string{"dog8", "dog6"} {
		gt := gt
		t.Run(gt, func(t *testing.T) {
			g, cfg := mustGame(t, gt)
			round, _, err := ToGameRound(g, cfg)
			if err != nil {
				t.Fatalf("ToGameRound: %v", err)
			}
			var back map[string]generators.Competitor
			if err := json.Unmarshal([]byte(round.CompetitorsJSON), &back); err != nil {
				t.Fatalf("unmarshal CompetitorsJSON: %v", err)
			}
			if len(back) != cfg.NumberCompetitor {
				t.Errorf("len(back)=%d, want %d", len(back), cfg.NumberCompetitor)
			}
			for i := 1; i <= cfg.NumberCompetitor; i++ {
				k := strconv.Itoa(i)
				if _, ok := back[k]; !ok {
					t.Errorf("CompetitorsJSON missing key %q", k)
				}
			}
		})
	}
}

func TestOddsJSONShape(t *testing.T) {
	for _, gt := range []string{"dog8", "dog6"} {
		gt := gt
		t.Run(gt, func(t *testing.T) {
			g, cfg := mustGame(t, gt)
			round, _, err := ToGameRound(g, cfg)
			if err != nil {
				t.Fatalf("ToGameRound: %v", err)
			}
			var full []float64
			if err := json.Unmarshal([]byte(round.OddsJSON), &full); err != nil {
				t.Fatalf("unmarshal OddsJSON: %v", err)
			}
			if got, want := len(full), cfg.NumberOdds; got != want {
				t.Errorf("len(OddsJSON)=%d, want %d", got, want)
			}
			var win []float64
			if err := json.Unmarshal([]byte(round.WinOddsJSON), &win); err != nil {
				t.Fatalf("unmarshal WinOddsJSON: %v", err)
			}
			if got, want := len(win), cfg.WinOddsCount; got != want {
				t.Errorf("len(WinOddsJSON)=%d, want %d", got, want)
			}
			for i := 0; i < cfg.WinOddsCount; i++ {
				if full[i] != win[i] {
					t.Errorf("WinOddsJSON[%d]=%v != OddsJSON[%d]=%v", i, win[i], i, full[i])
				}
			}
		})
	}
}

func TestVideoNameJSON(t *testing.T) {
	g, cfg := mustGame(t, "dog8")
	round, _, err := ToGameRound(g, cfg)
	if err != nil {
		t.Fatalf("ToGameRound: %v", err)
	}
	var vn struct {
		MP4 string `json:"mp4"`
		JPG string `json:"jpg"`
	}
	if err := json.Unmarshal([]byte(round.VideoNameJSON), &vn); err != nil {
		t.Fatalf("unmarshal VideoNameJSON: %v", err)
	}
	if !strings.HasPrefix(vn.MP4, "/.local/dog8/R") {
		t.Errorf("VideoNameJSON.mp4=%q, want prefix /.local/dog8/R", vn.MP4)
	}
	if !strings.HasPrefix(vn.JPG, "/.local/dog8/R") {
		t.Errorf("VideoNameJSON.jpg=%q, want prefix /.local/dog8/R", vn.JPG)
	}
	if !strings.HasSuffix(vn.MP4, ".mp4") {
		t.Errorf("VideoNameJSON.mp4=%q, want suffix .mp4", vn.MP4)
	}
	if !strings.HasSuffix(vn.JPG, ".jpg") {
		t.Errorf("VideoNameJSON.jpg=%q, want suffix .jpg", vn.JPG)
	}
}

func TestIntervalJSON(t *testing.T) {
	g, cfg := mustGame(t, "dog8")
	round, _, err := ToGameRound(g, cfg)
	if err != nil {
		t.Fatalf("ToGameRound: %v", err)
	}
	var back map[string]map[string]map[string]any
	if err := json.Unmarshal([]byte(round.IntervalJSON), &back); err != nil {
		t.Fatalf("unmarshal IntervalJSON: %v", err)
	}
	if len(back) != cfg.IntervalCount {
		t.Errorf("len(IntervalJSON)=%d, want IntervalCount=%d", len(back), cfg.IntervalCount)
	}
	// dog8 ships 2 checkpoints, each with sub-keys "1" and "2".
	for cp, inner := range back {
		if _, ok := inner["1"]; !ok {
			t.Errorf("checkpoint %q: missing inner key \"1\"", cp)
		}
		if _, ok := inner["2"]; !ok {
			t.Errorf("checkpoint %q: missing inner key \"2\"", cp)
		}
	}
}

func TestStatusAlwaysF(t *testing.T) {
	for _, gt := range []string{"dog8", "dog6"} {
		gt := gt
		t.Run(gt, func(t *testing.T) {
			g, cfg := mustGame(t, gt)
			round, _, err := ToGameRound(g, cfg)
			if err != nil {
				t.Fatalf("ToGameRound: %v", err)
			}
			if round.Status != "F" {
				t.Errorf("Status=%q, want F", round.Status)
			}
		})
	}
}

func TestAdapterErrorsOnMissingPosition(t *testing.T) {
	g, cfg := mustGame(t, "dog8")
	// Delete the entry for position 3.
	delete(g.Finish.Finish, "3")
	_, _, err := ToGameRound(g, cfg)
	if err == nil {
		t.Fatalf("expected error when finish position 3 is missing, got nil")
	}
	if !strings.Contains(err.Error(), "missing position 3") {
		t.Errorf("error=%q, want substring \"missing position 3\"", err.Error())
	}
}

// TestAdapterPreservesJackpotInfo checks JackpotInfoJSON round-trips
// cleanly back to JackpotInfo with the expected fields. Cheap, prevents
// silent breakage of the wire shape.
func TestAdapterPreservesJackpotInfo(t *testing.T) {
	g, cfg := mustGame(t, "dog8")
	round, _, err := ToGameRound(g, cfg)
	if err != nil {
		t.Fatalf("ToGameRound: %v", err)
	}
	var back generators.JackpotInfo
	if err := json.Unmarshal([]byte(round.JackpotInfoJSON), &back); err != nil {
		t.Fatalf("unmarshal JackpotInfoJSON: %v", err)
	}
	if back.BonusValue != g.JackpotInfo.BonusValue {
		t.Errorf("BonusValue=%v, want %v", back.BonusValue, g.JackpotInfo.BonusValue)
	}
	if back.OldBonusValue != g.JackpotInfo.OldBonusValue {
		t.Errorf("OldBonusValue=%v, want %v", back.OldBonusValue, g.JackpotInfo.OldBonusValue)
	}
	if len(back.BonusHistory) != 1 || back.BonusHistory[0].Name != "Virteon Gaming" {
		t.Errorf("BonusHistory=%+v, want [{Name=Virteon Gaming}]", back.BonusHistory)
	}
}
