package feed

import (
	"vg-racegen/internal/models"
	"vg-racegen/internal/sqlite"
)

// sqliteReader is the production Reader: a thin adapter over the
// package-level GA helpers in internal/sqlite. It is read-only — it never
// calls any Upsert/Save path. The relay.db connection is owned by the
// internal/sqlite package (opened once via sqlite.Init in cmd/feed).
type sqliteReader struct{}

// NewSQLiteReader returns the production Reader. sqlite.Init must have been
// called first so the package-level *sql.DB is live.
func NewSQLiteReader() Reader { return sqliteReader{} }

func (sqliteReader) GameByRoundCode(roundCode string) (*models.GameRound, error) {
	return sqlite.GameByRoundCodeGA(roundCode)
}

func (sqliteReader) ResultsByRoundCode(roundCode string) ([]models.GameResult, error) {
	return sqlite.ResultsByRoundCodeGA(roundCode)
}

func (sqliteReader) UpcomingGames(betofferID, limit int) ([]*models.GameRound, error) {
	return sqlite.UpcomingGamesGA(betofferID, limit)
}

func (sqliteReader) RecentResults(betofferID, limit int) ([]*models.GameRound, map[string][]models.GameResult, error) {
	return sqlite.RecentResultsGA(betofferID, limit)
}

func (sqliteReader) Ping() error {
	// A cheap readability probe: list 0 upcoming for dog8's betoffer. A
	// LIMIT 0 query still exercises the connection + the GameRounds table.
	_, err := sqlite.UpcomingGamesGA(541, 0)
	return err
}
