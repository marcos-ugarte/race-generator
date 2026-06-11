package feed

import (
	"testing"
	"time"

	"vg-racegen/internal/models"
)

func ptrF(f float64) *float64 { return &f }

// sampleRound returns a dog8-shaped GA round with video [start, end] and
// 3 competitors. results are the precomputed finish.
func sampleRound(start, end time.Time) (*models.GameRound, []models.GameResult) {
	g := &models.GameRound{
		RoundCode:        "GA541_105_202606110042",
		GameTypeID:       541,
		GameType:         "dog8",
		Status:           "F", // generator always persists F — gating must ignore this
		CompetitorsCount: 3,
		CompetitorsJSON:  `{"1":{"name":"Alpha"},"2":{"name":"Bravo"},"3":{"name":"Charlie"}}`,
		OddsJSON:         `[2.5,3.0,4.0]`,
		Bonus:            1,
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

func TestToPublic_GatingOpen(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	// Video ends in the future ⇒ open ⇒ NO finishOrder / payouts.
	g, results := sampleRound(now.Add(-30*time.Second), now.Add(30*time.Second))

	dto, err := ToPublic(g, results, now)
	if err != nil {
		t.Fatalf("ToPublic: %v", err)
	}
	if dto.State != "open" {
		t.Fatalf("state = %q, want open", dto.State)
	}
	if dto.FinishOrder != nil {
		t.Errorf("LEAK: finishOrder present on open round: %+v", dto.FinishOrder)
	}
	if dto.Payouts != nil {
		t.Errorf("LEAK: payouts present on open round: %+v", dto.Payouts)
	}
	if len(dto.Participants) != 3 {
		t.Fatalf("participants = %d, want 3", len(dto.Participants))
	}
	// Participants + winOdds still present pre-final.
	if dto.Participants[0].Name != "Alpha" || dto.Participants[0].WinOdds == nil || *dto.Participants[0].WinOdds != 2.5 {
		t.Errorf("participant[0] = %+v, want Alpha/2.5", dto.Participants[0])
	}
}

func TestToPublic_GatingFinal(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	// Video already ended ⇒ final ⇒ finishOrder + payouts present.
	g, results := sampleRound(now.Add(-90*time.Second), now.Add(-30*time.Second))

	dto, err := ToPublic(g, results, now)
	if err != nil {
		t.Fatalf("ToPublic: %v", err)
	}
	if dto.State != "final" {
		t.Fatalf("state = %q, want final", dto.State)
	}
	if len(dto.FinishOrder) != 3 {
		t.Fatalf("finishOrder = %d entries, want 3", len(dto.FinishOrder))
	}
	if dto.FinishOrder[0].Position != 1 || dto.FinishOrder[0].Dorsal != 2 {
		t.Errorf("winner = pos%d dorsal%d, want pos1 dorsal2", dto.FinishOrder[0].Position, dto.FinishOrder[0].Dorsal)
	}
	// FinishTime 0 (position 3) is omitted.
	if dto.FinishOrder[2].FinishTime != nil {
		t.Errorf("expected nil finishTime for zero, got %v", *dto.FinishOrder[2].FinishTime)
	}
	if len(dto.Payouts) != 3 {
		t.Errorf("payouts = %d, want 3", len(dto.Payouts))
	}
}

func TestToPublic_BoundaryIsFinalAtExactEnd(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	// now == VideoEndDt ⇒ final (now < end is false).
	g, results := sampleRound(now.Add(-60*time.Second), now)
	dto, err := ToPublic(g, results, now)
	if err != nil {
		t.Fatalf("ToPublic: %v", err)
	}
	if dto.State != "final" {
		t.Fatalf("at exact VideoEndDt state = %q, want final", dto.State)
	}
}

func TestToPublic_BonusClamp(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	g, results := sampleRound(now.Add(30*time.Second), now.Add(90*time.Second))
	g.Bonus = 0 // sentinel — must clamp to 1
	dto, _ := ToPublic(g, results, now)
	if dto.Bonus != 1 {
		t.Errorf("bonus = %d, want clamped to 1", dto.Bonus)
	}
}
