// Package adapter projects a generated Game (from internal/racegen/generators)
// onto the persistence-layer shapes (*models.GameRound and []models.GameResult).
//
// The adapter is deliberately the only racegen package that knows about
// internal/models. Phase 3 packages stay pure (no DB types) so they remain
// unit-testable without DB scaffolding.
//
// IMPORTANT: this adapter MUST set GameRound.GameType to the source
// gameType ("dog8" / "dog6") so that downstream multisource code does NOT
// fall back to GameTypeFromRoundCode — that fallback would inspect the
// "GA…" round-code prefix and mis-classify the round.
package adapter

import (
	"encoding/json"
	"fmt"
	"strconv"

	"vg-racegen/internal/models"
	"vg-racegen/internal/racegen/config"
	"vg-racegen/internal/racegen/generators"
)

// ToGameRound projects a generators.Game onto a *models.GameRound and a
// []models.GameResult. Returns an error if any finish position is missing
// (no silent skipping) or if any internal JSON marshal fails (which would
// indicate a programmer error in the upstream Game struct).
//
// The cfg argument is required to slice WinOddsJSON (the legacy schema
// stores the WIN-only prefix separately from the full odds vector). For
// dog8/dog6 cfg.WinOddsCount happens to equal NumberCompetitor; that
// equality is a coincidence and we don't rely on it.
//
// All timestamps are formatted "2006-01-02 15:04:05" (space, not 'T') to
// match the DS vendor wire format already in use across multisource.
// CreatedAt is deterministically set to VideoStartDt — emphatically NOT
// time.Now() — so a re-generated round byte-matches the original.
func ToGameRound(g generators.Game, cfg config.GameTypeConfigExt) (*models.GameRound, []models.GameResult, error) {
	// Validate up front so we fail fast on programmer errors.
	if g.RoundCode == "" {
		return nil, nil, fmt.Errorf("adapter: empty RoundCode")
	}
	if cfg.NumberCompetitor <= 0 {
		return nil, nil, fmt.Errorf("adapter: cfg.NumberCompetitor=%d, want > 0", cfg.NumberCompetitor)
	}
	if cfg.WinOddsCount <= 0 || cfg.WinOddsCount > len(g.Odds) {
		return nil, nil, fmt.Errorf("adapter: cfg.WinOddsCount=%d outside (0, len(Odds)=%d]",
			cfg.WinOddsCount, len(g.Odds))
	}

	// Marshal the JSON-shaped fields first so we can fail before allocating
	// the GameRound. Competitors are key-sorted via a stable map — Go's
	// json.Marshal on map[string]T sorts string keys lexicographically by
	// default, which gives us "1","2",…,"9" deterministically.
	// (For dog8/dog6 N≤8, so lex == numeric order. If we ever ship a
	// game type with N≥10, we'd need an explicit numeric sort to keep
	// the wire format stable — flagged here for future maintainers.)
	competitorsJSON, err := json.Marshal(g.Competitors)
	if err != nil {
		return nil, nil, fmt.Errorf("adapter: marshal Competitors: %w", err)
	}
	oddsJSON, err := json.Marshal(g.Odds)
	if err != nil {
		return nil, nil, fmt.Errorf("adapter: marshal Odds: %w", err)
	}
	winOddsJSON, err := json.Marshal(g.Odds[:cfg.WinOddsCount])
	if err != nil {
		return nil, nil, fmt.Errorf("adapter: marshal WinOdds: %w", err)
	}
	videoNameJSON, err := json.Marshal(g.Finish.VideoName)
	if err != nil {
		return nil, nil, fmt.Errorf("adapter: marshal VideoName: %w", err)
	}
	intervalJSON, err := json.Marshal(g.Finish.Interval)
	if err != nil {
		return nil, nil, fmt.Errorf("adapter: marshal Interval: %w", err)
	}
	jackpotJSON, err := json.Marshal(g.JackpotInfo)
	if err != nil {
		return nil, nil, fmt.Errorf("adapter: marshal JackpotInfo: %w", err)
	}

	const tsFormat = "2006-01-02 15:04:05"
	videoStartStr := g.VideoStartDt.UTC().Format(tsFormat)
	videoEndStr := g.VideoEndDt.UTC().Format(tsFormat)

	round := &models.GameRound{
		RoundCode:        g.RoundCode,
		GameTypeID:       g.BetofferID,
		GameType:         g.GameType,
		RaceNumber:       strconv.Itoa(g.IDRace),
		RaceDate:         g.VideoStartDt.UTC().Format("2006-01-02"),
		Status:           "F",
		CompetitorsCount: len(g.Competitors),
		CompetitorsJSON:  string(competitorsJSON),
		OddsJSON:         string(oddsJSON),
		WinOddsJSON:      string(winOddsJSON),
		Bonus:            g.Bonus,
		VideoName:        g.Finish.VideoName.MP4,
		VideoNameJSON:    string(videoNameJSON),
		Weather:          g.Conditions.Weather,
		Temperature:      g.Conditions.Temperature,
		Humidity:         g.Conditions.Humidity,
		Wind:             g.Conditions.Wind,
		CourseConditions: g.Conditions.CourseConditions,
		IntervalJSON:     string(intervalJSON),
		JackpotInfoJSON:  string(jackpotJSON),
		VideoStartDt:     videoStartStr,
		VideoEndDt:       videoEndStr,
		RoundInterval:    g.RoundInterval,
		ScheduledAt:      videoStartStr,
		CreatedAt:        videoStartStr,
		FinishedAt:       &videoEndStr,
	}

	// Build GameResult slice — one entry per finishing position, ordered
	// 1..N. Hard error on missing position so we never silently emit a
	// round with a gap in the finish.
	results := make([]models.GameResult, 0, cfg.NumberCompetitor)
	for pos := 1; pos <= cfg.NumberCompetitor; pos++ {
		key := strconv.Itoa(pos)
		fp, ok := g.Finish.Finish[key]
		if !ok {
			return nil, nil, fmt.Errorf("adapter: finish missing position %d (key=%q)", pos, key)
		}
		results = append(results, models.GameResult{
			GameRoundID:  g.RoundCode,
			Position:     pos,
			RunnerNumber: fp.CompetitorIndex,
			FinishTime:   fp.Time,
		})
	}

	return round, results, nil
}
