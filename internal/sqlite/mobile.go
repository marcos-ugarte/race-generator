// mobile.go — separate read-mostly handle for ds-pos-mobile.db.
//
// The mobile DB is populated by cmd/collector-ds-pos-mobile (untracked at
// the time of writing; another agent is wiring its writer path). The
// /web-ds WS endpoint (this branch — feature/web-ds-f1) only READS from
// this file: it serves replicated mobile-vendor frames to public WS
// clients, with the canonical mobile-vendor wire shape (vendor CloudFront
// videonames, full jackpotInfo etc.).
//
// Design notes:
//   - Schema mirrors ds-capture/relay.db (same CREATE TABLE + ALTER
//     migrations). The `db` and prepared statements in sqlite.go are
//     reserved for the relay file; here we keep a completely independent
//     *sql.DB so the two handles can't interfere.
//   - All read helpers exposed in this file are package-level functions
//     (MobileXxx) instead of methods on a struct, matching the pattern
//     used by the existing sqlite.GamesByRoundCodes / sqlite.ResultsBatch
//     style. The wsserver/web_ds code calls them directly.
//   - InitMobile is non-fatal at the caller: cmd/tv-broadcaster swallows
//     errors so the box still starts when the mobile file is missing
//     (e.g. before the mobile collector has been deployed).
package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	"vg-racegen/internal/models"

	_ "modernc.org/sqlite"
)

// ErrMobileDBClosed is returned by every Mobile* helper when InitMobile was
// never called (or failed). Callers can errors.Is-check this if they want
// to distinguish "DB not configured" from real query errors; the wsserver
// path simply logs and continues without an init.
var ErrMobileDBClosed = errors.New("mobile DB not initialised")

// dbMobile is the package-level handle for ds-pos-mobile.db. Opened by
// InitMobile, used by all Mobile* helpers below. nil when InitMobile was
// not called or failed.
var dbMobile *sql.DB

// DBMobile returns the mobile-DB connection pool. Returns nil if InitMobile
// wasn't called or failed.
func DBMobile() *sql.DB { return dbMobile }

// InitMobile opens (or creates if missing) the mobile DB.
//
// The schema mirrors createTableSQL exactly so a CI fingerprint guard
// between this file and ds-capture/internal/sqlite/sqlite.go::Fingerprint
// stays meaningful. Migrations are applied idempotently — they no-op on
// existing files, and Init for the first time produces the same shape
// as the relay DB.
//
// WAL + busy_timeout pragmas keep readers concurrent with the mobile
// collector's writer process.
func InitMobile(dbPath string) error {
	// Same fail-fast contract as Init(): the file MUST exist (ds-capture
	// is the canonical creator). Without the gate, modernc.org/sqlite
	// would create a fresh empty ds-pos-mobile.db at the symlink target
	// and the mobile collector would later find a schema mismatch.
	if err := requireExistingDBFile(dbPath); err != nil {
		return err
	}
	// busy_timeout via DSN so it is installed by the driver inside
	// conn.Open, before any user-level Exec runs. Same rationale as
	// Init() in sqlite.go: postfacto pragmas race on cold-start when
	// multiple processes (tv-broadcaster + collector-ds-pos-mobile)
	// open the same fresh ds-pos-mobile.db in parallel.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", dbPath)
	mobile, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("mobile sqlite open: %w", err)
	}

	// journal_mode=WAL needs an EXCLUSIVE lock that bypasses
	// busy_timeout while another connection is mid-transition. Use
	// the same explicit retry loop as Init() in sqlite.go.
	if err := applyWALWithRetry(mobile); err != nil {
		_ = mobile.Close()
		return fmt.Errorf("mobile sqlite pragma WAL: %w", err)
	}
	if _, err := mobile.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		_ = mobile.Close()
		return fmt.Errorf("mobile sqlite pragma synchronous: %w", err)
	}

	if _, err := mobile.Exec(createTableSQL); err != nil {
		_ = mobile.Close()
		return fmt.Errorf("mobile sqlite create tables: %w", err)
	}
	for _, m := range alterMigrations {
		_, _ = mobile.Exec(m) // ignore "duplicate column" on subsequent opens
	}
	if _, err := mobile.Exec(uniqueIndexDDL); err != nil {
		_ = mobile.Close()
		return fmt.Errorf("mobile sqlite unique index: %w", err)
	}

	dbMobile = mobile
	log.Printf("[mobile] InitMobile ok path=%s schemaFingerprint=%s", dbPath, Fingerprint())
	return nil
}

// CloseMobile shuts down the mobile DB handle if it was opened.
// Safe to call multiple times / when InitMobile failed.
func CloseMobile() error {
	if dbMobile == nil {
		return nil
	}
	err := dbMobile.Close()
	dbMobile = nil
	return err
}

// gameRoundSelectCols centralises the column list used by all Mobile*
// scanGameRound helpers. Keep in lock-step with scanGameRound() in
// sqlite.go — the column order must match.
const gameRoundSelectCols = `
	RoundCode, GameTypeId, GameType, RaceNumber, RaceDate, Status,
	CompetitorsCount, CompetitorsJson, OddsJson, WinOddsJson, Bonus,
	VideoName, COALESCE(VideoNameJson,''), Weather, Temperature, Humidity, Wind, CourseConditions,
	IntervalJson, COALESCE(JackpotInfoJson,''), VideoStartDt, VideoEndDt, RoundInterval, ScheduledAt,
	CreatedAt, FinishedAt
`

// MobileGameByRoundCode fetches a single game round from the mobile DB.
// Returns nil, sql.ErrNoRows when the code does not exist.
func MobileGameByRoundCode(roundCode string) (*models.GameRound, error) {
	if dbMobile == nil {
		return nil, ErrMobileDBClosed
	}
	// #nosec G201 — column list is a static const above; only roundCode is bound.
	row := dbMobile.QueryRow(
		"SELECT "+gameRoundSelectCols+" FROM GameRounds WHERE RoundCode=?",
		roundCode,
	)
	return scanGameRound(row)
}

// MobileGamesByRoundCodes fetches multiple game rounds from the mobile DB
// in a single query. Codes that do not exist are silently omitted.
// Results are sorted by VideoStartDt ASC.
func MobileGamesByRoundCodes(codes []string) ([]*models.GameRound, error) {
	if dbMobile == nil {
		return nil, ErrMobileDBClosed
	}
	if len(codes) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(codes))
	args := make([]interface{}, len(codes))
	for i, c := range codes {
		placeholders[i] = "?"
		args[i] = c
	}

	// #nosec G201 — placeholders is a generated "?,?,..." list; values are via args.
	query := fmt.Sprintf(`
		SELECT `+gameRoundSelectCols+`
		FROM GameRounds
		WHERE RoundCode IN (%s)
		ORDER BY VideoStartDt ASC
	`, strings.Join(placeholders, ","))

	rows, err := dbMobile.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("mobile query by round codes: %w", err)
	}
	defer rows.Close()
	return scanGameRounds(rows)
}

// MobileResultsBatch fetches GameResults for multiple round codes from the
// mobile DB in a single query. Returns map keyed by round code. Codes
// with no results are omitted from the map (caller may treat as nil).
func MobileResultsBatch(roundCodes []string) (map[string][]models.GameResult, error) {
	if dbMobile == nil {
		return nil, ErrMobileDBClosed
	}
	if len(roundCodes) == 0 {
		return make(map[string][]models.GameResult), nil
	}

	placeholders := make([]string, len(roundCodes))
	args := make([]interface{}, len(roundCodes))
	for i, rc := range roundCodes {
		placeholders[i] = "?"
		args[i] = rc
	}

	// #nosec G201 — placeholders generated above; values via args.
	query := fmt.Sprintf(
		"SELECT GameRoundId, Position, RunnerNumber, FinishTime FROM GameResults WHERE GameRoundId IN (%s) ORDER BY GameRoundId, Position",
		strings.Join(placeholders, ","),
	)

	rows, err := dbMobile.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("mobile query results batch: %w", err)
	}
	defer rows.Close()

	results := make(map[string][]models.GameResult)
	for rows.Next() {
		var r models.GameResult
		if err := rows.Scan(&r.GameRoundID, &r.Position, &r.RunnerNumber, &r.FinishTime); err != nil {
			return nil, fmt.Errorf("mobile scan result: %w", err)
		}
		results[r.GameRoundID] = append(results[r.GameRoundID], r)
	}
	return results, rows.Err()
}

// MobileGamesWindow returns history (finished) + future (open) games for
// a betoffer from the mobile DB. Mirrors the relay-side GamesWindow.
// Results are sorted by VideoStartDt ASC (oldest history first, then
// open races chronologically).
func MobileGamesWindow(betofferID int, historyCount, futureCount int) ([]*models.GameRound, error) {
	if dbMobile == nil {
		return nil, ErrMobileDBClosed
	}

	// History: newest first, then reversed to ASC.
	// #nosec G201 — column list is a constant; only ? params are bound.
	historyRows, err := dbMobile.Query(
		"SELECT "+gameRoundSelectCols+
			" FROM GameRounds WHERE GameTypeId=? AND Status='F' ORDER BY VideoStartDt DESC LIMIT ?",
		betofferID, abs(historyCount),
	)
	if err != nil {
		return nil, fmt.Errorf("mobile query history: %w", err)
	}
	history, err := scanGameRounds(historyRows)
	historyRows.Close()
	if err != nil {
		return nil, fmt.Errorf("mobile scan history: %w", err)
	}
	// Reverse so result is ASC.
	for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
		history[i], history[j] = history[j], history[i]
	}

	// Future: ASC by VideoStartDt.
	// #nosec G201 — same rationale.
	futureRows, err := dbMobile.Query(
		"SELECT "+gameRoundSelectCols+
			" FROM GameRounds WHERE GameTypeId=? AND Status='O' ORDER BY VideoStartDt ASC LIMIT ?",
		betofferID, abs(futureCount),
	)
	if err != nil {
		return nil, fmt.Errorf("mobile query future: %w", err)
	}
	future, err := scanGameRounds(futureRows)
	futureRows.Close()
	if err != nil {
		return nil, fmt.Errorf("mobile scan future: %w", err)
	}

	result := make([]*models.GameRound, 0, len(history)+len(future))
	result = append(result, history...)
	result = append(result, future...)
	return result, nil
}

// MobileStatsHistory mirrors StatsHistory: returns the last N finished
// rounds for a betoffer as [[1st,2nd,3rd,...], ...] arrays of runner
// numbers, plus the newest and oldest round codes.
//
// Used by web_ds_betoffers.go to populate setting.betoffers[i].stats.
func MobileStatsHistory(betofferID int, limit int) (history [][]int, newest string, oldest string, err error) {
	if dbMobile == nil {
		return nil, "", "", ErrMobileDBClosed
	}

	rows, err := dbMobile.Query(`
		SELECT RoundCode
		FROM GameRounds
		WHERE GameTypeId=? AND Status='F'
		ORDER BY VideoStartDt DESC
		LIMIT ?
	`, betofferID, limit)
	if err != nil {
		return nil, "", "", fmt.Errorf("mobile query stats history: %w", err)
	}
	defer rows.Close()

	var roundCodes []string
	for rows.Next() {
		var rc string
		if err := rows.Scan(&rc); err != nil {
			return nil, "", "", fmt.Errorf("mobile scan stats history: %w", err)
		}
		roundCodes = append(roundCodes, rc)
	}
	if err := rows.Err(); err != nil {
		return nil, "", "", err
	}
	if len(roundCodes) == 0 {
		return [][]int{}, "", "", nil
	}

	newest = roundCodes[0]
	oldest = roundCodes[len(roundCodes)-1]

	resultMap, err := MobileResultsBatch(roundCodes)
	if err != nil {
		return nil, "", "", fmt.Errorf("mobile results for stats: %w", err)
	}

	history = make([][]int, 0, len(roundCodes))
	for _, rc := range roundCodes {
		results := resultMap[rc]
		positions := make([]int, len(results))
		for _, r := range results {
			if r.Position >= 1 && r.Position <= len(positions) {
				positions[r.Position-1] = r.RunnerNumber
			}
		}
		history = append(history, positions)
	}
	return history, newest, oldest, nil
}
