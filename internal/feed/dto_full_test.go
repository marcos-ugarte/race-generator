package feed

import (
	"testing"
	"time"

	"vg-racegen/internal/models"
)

// sampleFullRound returns a dog8-shaped GA round with a videoName JSON and
// 3 competitors. Mirrors sampleRound but adds the fields the FULL DTO
// needs (VideoNameJSON, RoundInterval, weather).
func sampleFullRound(start, end time.Time) (*models.GameRound, []models.GameResult) {
	g := &models.GameRound{
		RoundCode:        "GA541_105_202606110042",
		GameTypeID:       541,
		GameType:         "dog8",
		Status:           "F", // generator always persists F — gating must ignore this
		CompetitorsCount: 3,
		CompetitorsJSON:  `{"1":{"name":"Alpha","weight":38,"numberOfRaces":8,"strikeRate":13.69},"2":{"name":"Bravo","weight":27.6,"numberOfRaces":5,"strikeRate":12.12},"3":{"name":"Charlie","weight":26.7,"numberOfRaces":1,"strikeRate":19.2}}`,
		OddsJSON:         `[2.5,3.0,4.0,9.9,9.9,9.9]`,
		VideoNameJSON:    `{"mp4":"/.local/dog8/0021345_crf27.mp4","jpg":"/.local/dog8/0021345_crf27.jpg"}`,
		IntervalJSON:     `{"1":{"1":{"competitorIndex":2,"time":7.6},"2":{"competitorIndex":1,"time":7.83}}}`,
		JackpotInfoJSON:  `{"bonusValue":48765.08,"oldBonusValue":48756.43,"bonusHistory":[]}`,
		Bonus:            1,
		Weather:          "sunny",
		Temperature:      21,
		Humidity:         55,
		Wind:             "NE 3",
		RoundInterval:    240,
		VideoStartDt:     start.UTC().Format(dtFormat),
		VideoEndDt:       end.UTC().Format(dtFormat),
	}
	results := []models.GameResult{
		{GameRoundID: g.RoundCode, Position: 1, RunnerNumber: 2, FinishTime: ptrF(29.1)},
		{GameRoundID: g.RoundCode, Position: 2, RunnerNumber: 1, FinishTime: ptrF(29.4)},
		{GameRoundID: g.RoundCode, Position: 3, RunnerNumber: 3},
	}
	return g, results
}

// TestToFull_GatingBetting: before VideoStartDt the round is in "betting"
// — videoName / finishOrder / payouts MUST be absent (winner not leaked),
// but odds + participants are present.
func TestToFull_GatingBetting(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	// Video starts in the future ⇒ betting.
	g, results := sampleFullRound(now.Add(30*time.Second), now.Add(270*time.Second))

	dto, err := ToFull(g, results, now)
	if err != nil {
		t.Fatalf("ToFull: %v", err)
	}
	if dto.State != "betting" {
		t.Fatalf("state = %q, want betting", dto.State)
	}
	if dto.VideoName != nil {
		t.Errorf("LEAK: videoName present on betting round: %s", dto.VideoName)
	}
	if dto.FinishOrder != nil {
		t.Errorf("LEAK: finishOrder present on betting round: %+v", dto.FinishOrder)
	}
	if dto.Payouts != nil {
		t.Errorf("LEAK: payouts present on betting round: %+v", dto.Payouts)
	}
	// Odds + participants ARE public during betting.
	if dto.Odds == nil {
		t.Errorf("odds missing on betting round (should be public)")
	}
	if len(dto.Participants) != 3 {
		t.Fatalf("participants = %d, want 3", len(dto.Participants))
	}
	// competitors + jackpotInfo are public during betting (full passthrough).
	if string(dto.Competitors) != g.CompetitorsJSON {
		t.Errorf("competitors = %s, want verbatim passthrough %s", dto.Competitors, g.CompetitorsJSON)
	}
	if string(dto.JackpotInfo) != g.JackpotInfoJSON {
		t.Errorf("jackpotInfo = %s, want verbatim passthrough %s", dto.JackpotInfo, g.JackpotInfoJSON)
	}
	// interval is GATED — must be absent during betting (reveals progression).
	if dto.Interval != nil {
		t.Errorf("LEAK: interval present on betting round: %s", dto.Interval)
	}
	if dto.Betoffer != 541 {
		t.Errorf("betoffer = %d, want 541", dto.Betoffer)
	}
	if dto.RoundInterval != 240 {
		t.Errorf("roundInterval = %d, want 240", dto.RoundInterval)
	}
}

// TestToFull_GatingRunning: at/after VideoStartDt the round is "running" —
// videoName + finishOrder + payouts ARE revealed.
func TestToFull_GatingRunning(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	// Video started 30s ago, still running (ends in 210s) ⇒ running.
	g, results := sampleFullRound(now.Add(-30*time.Second), now.Add(210*time.Second))

	dto, err := ToFull(g, results, now)
	if err != nil {
		t.Fatalf("ToFull: %v", err)
	}
	if dto.State != "running" {
		t.Fatalf("state = %q, want running", dto.State)
	}
	if dto.VideoName == nil {
		t.Errorf("videoName missing on running round")
	}
	if len(dto.FinishOrder) != 3 {
		t.Fatalf("finishOrder = %d entries, want 3", len(dto.FinishOrder))
	}
	if dto.FinishOrder[0].Position != 1 || dto.FinishOrder[0].Dorsal != 2 {
		t.Errorf("winner = pos%d dorsal%d, want pos1 dorsal2", dto.FinishOrder[0].Position, dto.FinishOrder[0].Dorsal)
	}
	if len(dto.Payouts) == 0 {
		t.Errorf("payouts missing on running round")
	}
	// Full odds vector passed through verbatim (all 6 entries).
	if dto.Odds == nil || string(dto.Odds) != `[2.5,3.0,4.0,9.9,9.9,9.9]` {
		t.Errorf("odds = %s, want full passthrough", dto.Odds)
	}
	// competitors / jackpotInfo / interval all present + verbatim once running.
	if string(dto.Competitors) != g.CompetitorsJSON {
		t.Errorf("competitors = %s, want verbatim passthrough %s", dto.Competitors, g.CompetitorsJSON)
	}
	if string(dto.JackpotInfo) != g.JackpotInfoJSON {
		t.Errorf("jackpotInfo = %s, want verbatim passthrough %s", dto.JackpotInfo, g.JackpotInfoJSON)
	}
	if string(dto.Interval) != g.IntervalJSON {
		t.Errorf("interval = %s, want verbatim passthrough %s", dto.Interval, g.IntervalJSON)
	}
}

// TestToFull_BoundaryRunningAtExactStart: now == VideoStartDt ⇒ running
// (betting-close is inclusive of the boundary, same as the /tv reveal).
func TestToFull_BoundaryRunningAtExactStart(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	g, results := sampleFullRound(now, now.Add(240*time.Second))
	dto, err := ToFull(g, results, now)
	if err != nil {
		t.Fatalf("ToFull: %v", err)
	}
	if dto.State != "running" {
		t.Fatalf("at exact VideoStartDt state = %q, want running", dto.State)
	}
	if dto.VideoName == nil {
		t.Errorf("videoName must be revealed at exact VideoStartDt")
	}
}

// TestToFull_BareVideoNameWrapped: a DS-style bare basename is wrapped into
// an object once running.
func TestToFull_BareVideoNameWrapped(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	g, results := sampleFullRound(now.Add(-10*time.Second), now.Add(230*time.Second))
	g.VideoNameJSON = ""
	g.VideoName = "R0029"
	dto, err := ToFull(g, results, now)
	if err != nil {
		t.Fatalf("ToFull: %v", err)
	}
	if dto.VideoName == nil || string(dto.VideoName) != `{"name":"R0029"}` {
		t.Errorf("videoName = %s, want {\"name\":\"R0029\"}", dto.VideoName)
	}
}
