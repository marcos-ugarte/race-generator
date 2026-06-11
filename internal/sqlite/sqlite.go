package sqlite

import (
	"crypto/md5" //nolint:gosec // G501: used as a non-cryptographic content hash (dedup key)
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"vg-racegen/internal/models"

	_ "modernc.org/sqlite"
)

// requireExistingDBFile gates VG's SQLite Init / InitMobile against the
// silent O_CREATE behaviour of modernc.org/sqlite: opening
// "file:/data/<missing>.db?..." would otherwise spawn an empty file at
// the symlink target and confuse the writer (ds-capture's collectors)
// once it comes back online.
//
// Post-cutover (2026-05-17) ds-capture is the canonical creator of
// these files. VG only consumes them. When deployed in that topology
// the operator sets VIRTUALES_DB_REQUIRE_EXISTING=1 and a missing file
// at boot fails fast — better than corrupting the source-of-truth by
// creating an empty schema-only sibling that ds-capture later opens.
//
// Opt-in by design: with the env unset (default), the legacy
// create-on-missing behaviour is preserved so first-deploy bootstrap
// and the existing test fixtures keep working without modification.
// The cutover-aware docker-compose sets the flag for api,
// tv-broadcaster, and audit-verifier — see the override file shipped
// with the cutover branch.
func requireExistingDBFile(path string) error {
	if os.Getenv("VIRTUALES_DB_REQUIRE_EXISTING") != "1" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("sqlite: %s does not exist and VIRTUALES_DB_REQUIRE_EXISTING=1 (post-cutover ds-capture is the canonical creator — pre-create the file or unset the env to bypass)", path)
		}
		return fmt.Errorf("sqlite: stat %s: %w", path, err)
	}
	return nil
}

// Fingerprint returns a stable SHA-256 of the SCHEMA contract: the CREATE
// TABLE DDL plus every ALTER migration (sorted) plus the UNIQUE INDEX.
// Pair with ds-capture/internal/sqlite.Fingerprint(); CI fails if the two
// hashes drift. See ds-capture/docs/ARCHITECTURE.md §"Schema contract".
func Fingerprint() string {
	parts := []string{
		normalizeDDL(createTableSQL),
		normalizeDDL(uniqueIndexDDL),
	}
	sorted := append([]string(nil), alterMigrations...)
	sort.Strings(sorted)
	parts = append(parts, sorted...)
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])
}

func normalizeDDL(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}

var db *sql.DB

// DB returns the shared SQLite connection pool. Returns nil if Init wasn't called.
// Used by MCP tools and other read-only consumers that need raw access.
func DB() *sql.DB { return db }

// dbRO is a separate, read-only *sql.DB pool opened with `mode=ro` so a
// query bypassing the regex deny-lists still cannot mutate the file.
// Audit finding C1.
var dbRO *sql.DB

// DBReadOnly returns the read-only SQLite connection pool. Returns nil
// until InitReadOnly succeeds.
func DBReadOnly() *sql.DB { return dbRO }

// InitReadOnly opens a second connection to the same SQLite database in
// read-only mode (URI flag `mode=ro`). Any attempt to INSERT/UPDATE/
// DELETE/ATTACH through this handle is rejected by SQLite itself —
// providing defense-in-depth on top of the validateSelect filter used by
// the MCP sqlite_query tool.
//
// Safe to call after Init. Stores the handle in package-level dbRO.
func InitReadOnly(path string) error {
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", path)
	roDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("sqlite open ro: %w", err)
	}
	if err := roDB.Ping(); err != nil {
		_ = roDB.Close()
		return fmt.Errorf("sqlite ping ro: %w", err)
	}
	dbRO = roDB
	return nil
}

// Prepared statements cached at package level.
var (
	stmtUpsert        *sql.Stmt
	stmtFinish        *sql.Stmt
	stmtInsertResult  *sql.Stmt
	stmtHistory       *sql.Stmt
	stmtFuture        *sql.Stmt
	stmtAfterGame     *sql.Stmt
	stmtStats         *sql.Stmt
	stmtStatsHist     *sql.Stmt
	stmtPatchFinished *sql.Stmt
)

const createTableSQL = `
CREATE TABLE IF NOT EXISTS GameRounds (
	RoundCode TEXT PRIMARY KEY,
	GameTypeId INT,
	GameType TEXT,
	RaceNumber TEXT,
	RaceDate TEXT,
	Status TEXT DEFAULT 'O',
	CompetitorsCount INT,
	CompetitorsJson TEXT,
	OddsJson TEXT,
	WinOddsJson TEXT,
	Bonus INT,
	VideoName TEXT,
	Weather TEXT,
	Temperature INT,
	Humidity INT,
	Wind TEXT,
	CourseConditions TEXT,
	IntervalJson TEXT,
	VideoStartDt TEXT,
	VideoEndDt TEXT,
	RoundInterval INT,
	ScheduledAt TEXT,
	CreatedAt TEXT DEFAULT CURRENT_TIMESTAMP,
	FinishedAt TEXT
);

CREATE TABLE IF NOT EXISTS GameResults (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	GameRoundId TEXT,
	Position INT,
	RunnerNumber INT,
	FinishTime REAL
);

CREATE INDEX IF NOT EXISTS idx_gr_type_status ON GameRounds(GameTypeId, Status, VideoStartDt);
CREATE INDEX IF NOT EXISTS idx_gr_roundcode ON GameResults(GameRoundId);
`

// alterMigrations are idempotent column additions. Run on every Init —
// "duplicate column" errors are expected and ignored.
//
// Hoisted to package scope (was a local slice in Init) so the
// schema-fingerprint guard can hash it. Keep in lock-step with
// /home/claude/projects/ds-capture/internal/sqlite/sqlite.go::alterMigrations.
var alterMigrations = []string{
	"ALTER TABLE GameRounds ADD COLUMN VideoNameJson TEXT DEFAULT ''",
	"ALTER TABLE GameRounds ADD COLUMN JackpotInfoJson TEXT DEFAULT ''",
	// 2026-05-25: index for the per-gameType MAX(VideoStartDt)
	// lookup in OverviewAll. Eliminates the per-group sort that
	// was driving the SQLite-backed raceapi to ~10 cores at 1 Hz
	// polling. Lock-step with ds-capture/internal/sqlite/sqlite.go.
	"CREATE INDEX IF NOT EXISTS idx_gr_gametype_videostart ON GameRounds(GameType, VideoStartDt)",
}

// uniqueIndexDDL guards against the duplicate-result bug fixed 2026-05-05.
// Hoisted (was inline in Init) so Fingerprint can hash it.
// Keep byte-identical to ds-capture's copy.
const uniqueIndexDDL = `
CREATE UNIQUE INDEX IF NOT EXISTS uniq_game_results_round_pos
 ON GameResults(GameRoundId, Position)
`

// walRetries / walRetryDelay control the explicit application-level
// retry loop around PRAGMA journal_mode=WAL. The WAL transition needs
// an EXCLUSIVE lock that SQLite intentionally bypasses busy_timeout
// for (documented behaviour of the WAL pragma — it cannot wait on
// another connection that is itself mid-transition into WAL mode).
// busy_timeout via DSN closes the race on every OTHER pragma; the
// WAL pragma needs explicit retries on our side.
//
// Total budget: 50 × 100ms = 5s, matching the busy_timeout budget so
// the two failure modes converge to the same wall-clock ceiling.
const (
	walRetries    = 50
	walRetryDelay = 100 * time.Millisecond
)

// isBusyError returns true if err is the SQLITE_BUSY / "database is
// locked" signal that surfaces during the cold-start WAL race. Match
// both the textual form ("database is locked") and the symbolic name
// ("SQLITE_BUSY") because modernc.org/sqlite formats errors as
// "<msg> (<code>) (<SYMBOL>)" and we want to be robust to whichever
// part of the string is present in any future driver version.
func isBusyError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "database is locked") ||
		strings.Contains(s, "SQLITE_BUSY")
}

// applyWALWithRetry runs PRAGMA journal_mode=WAL with an explicit
// retry loop. See walRetries/walRetryDelay doc above for the rationale.
// Returned error is the LAST attempt's error when retries exhaust.
func applyWALWithRetry(d *sql.DB) error {
	var err error
	for i := 0; i < walRetries; i++ {
		_, err = d.Exec("PRAGMA journal_mode=WAL")
		if err == nil {
			return nil
		}
		if !isBusyError(err) {
			return err
		}
		if i == walRetries-1 {
			break
		}
		time.Sleep(walRetryDelay)
	}
	return err
}

// Init opens the SQLite database. Post-cutover the file MUST already
// exist (ds-capture creates and writes the canonical schema); see
// requireExistingDBFile for the rationale and the test escape hatch.
// Migrations (CREATE/ALTER/UNIQUE INDEX) remain idempotent so opening
// over an existing populated file is a no-op.
func Init(dbPath string) error {
	if err := requireExistingDBFile(dbPath); err != nil {
		return err
	}
	var err error
	// busy_timeout goes via the DSN, not via PRAGMA, because the
	// modernc.org/sqlite driver sorts and applies _pragma values
	// inside conn.Open BEFORE any user-level db.Exec runs (see
	// gitlab.com/cznic/sqlite/-/issues/198). The PRAGMA-based code
	// path raced on cold-start: postfacto pragmas were queued AFTER
	// the WAL pragma so the busy_timeout never got a chance to
	// install before the WAL transition lost the lock race.
	//
	// Note: busy_timeout via DSN closes the race for every pragma
	// EXCEPT journal_mode=WAL itself — SQLite's WAL transition holds
	// an EXCLUSIVE lock that bypasses busy_timeout when another
	// connection is mid-transition. That residual race is handled by
	// applyWALWithRetry below.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", dbPath)
	db, err = sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("sqlite open: %w", err)
	}

	// Apply WAL with explicit retries to absorb the cold-start race
	// (see applyWALWithRetry doc).
	if err := applyWALWithRetry(db); err != nil {
		return fmt.Errorf("sqlite pragma WAL: %w", err)
	}
	// synchronous=NORMAL needs no exclusive lock, so the DSN-level
	// busy_timeout is enough.
	if _, err := db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		return fmt.Errorf("sqlite pragma synchronous: %w", err)
	}

	if _, err := db.Exec(createTableSQL); err != nil {
		return fmt.Errorf("sqlite create tables: %w", err)
	}

	for _, m := range alterMigrations {
		_, _ = db.Exec(m) // Ignore errors (column already exists).
	}

	// Dedupe + UNIQUE INDEX on GameResults(GameRoundId, Position).
	//
	// Until 2026-05-05 the schema had no uniqueness constraint and the
	// sniffer's `INSERT INTO` was repeated on reconnect (init re-asks
	// `historyGames=-10` and the gameResult events are reprocessed),
	// leaving ~9% of rounds with two identical rows per position.
	// Concretely the MCP saw duplicates in any GameResults query.
	//
	// Idempotent: the DELETE keeps the lowest id per (GameRoundId,
	// Position) and removes the rest. On a fresh DB it is a no-op
	// because every row is its own MIN(id) within its group. Then the
	// CREATE UNIQUE INDEX is also no-op once present.
	//
	// Run BEFORE prepareStatements so stmtInsertResult uses INSERT OR
	// IGNORE against the now-unique index.
	if _, err := db.Exec(`
		DELETE FROM GameResults
		 WHERE id NOT IN (
		     SELECT MIN(id) FROM GameResults
		      GROUP BY GameRoundId, Position
		 )
	`); err != nil {
		return fmt.Errorf("sqlite dedupe GameResults: %w", err)
	}
	if _, err := db.Exec(uniqueIndexDDL); err != nil {
		return fmt.Errorf("sqlite create unique index: %w", err)
	}

	if err := prepareStatements(); err != nil {
		return fmt.Errorf("sqlite prepare: %w", err)
	}

	return nil
}

func prepareStatements() error {
	var err error

	stmtUpsert, err = db.Prepare(`
		INSERT OR REPLACE INTO GameRounds (
			RoundCode, GameTypeId, GameType, RaceNumber, RaceDate, Status,
			CompetitorsCount, CompetitorsJson, OddsJson, WinOddsJson, Bonus,
			VideoName, VideoNameJson, Weather, Temperature, Humidity, Wind, CourseConditions,
			IntervalJson, JackpotInfoJson, VideoStartDt, VideoEndDt, RoundInterval, ScheduledAt,
			CreatedAt, FinishedAt
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`)
	if err != nil {
		return err
	}

	stmtFinish, err = db.Prepare(`
		UPDATE GameRounds SET Status='F', FinishedAt=? WHERE RoundCode=?
	`)
	if err != nil {
		return err
	}

	// INSERT OR IGNORE relies on the UNIQUE INDEX uniq_game_results_round_pos
	// added in Init(). Reconnect-driven re-sends of the same gameResult
	// silently no-op instead of duplicating the row.
	stmtInsertResult, err = db.Prepare(`
		INSERT OR IGNORE INTO GameResults (GameRoundId, Position, RunnerNumber, FinishTime)
		VALUES (?,?,?,?)
	`)
	if err != nil {
		return err
	}

	stmtHistory, err = db.Prepare(`
		SELECT RoundCode, GameTypeId, GameType, RaceNumber, RaceDate, Status,
			CompetitorsCount, CompetitorsJson, OddsJson, WinOddsJson, Bonus,
			VideoName, COALESCE(VideoNameJson,''), Weather, Temperature, Humidity, Wind, CourseConditions,
			IntervalJson, COALESCE(JackpotInfoJson,''), VideoStartDt, VideoEndDt, RoundInterval, ScheduledAt,
			CreatedAt, FinishedAt
		FROM GameRounds
		WHERE GameTypeId=? AND Status='F'
		ORDER BY VideoStartDt DESC
		LIMIT ?
	`)
	if err != nil {
		return err
	}

	stmtFuture, err = db.Prepare(`
		SELECT RoundCode, GameTypeId, GameType, RaceNumber, RaceDate, Status,
			CompetitorsCount, CompetitorsJson, OddsJson, WinOddsJson, Bonus,
			VideoName, COALESCE(VideoNameJson,''), Weather, Temperature, Humidity, Wind, CourseConditions,
			IntervalJson, COALESCE(JackpotInfoJson,''), VideoStartDt, VideoEndDt, RoundInterval, ScheduledAt,
			CreatedAt, FinishedAt
		FROM GameRounds
		WHERE GameTypeId=? AND Status='O'
		ORDER BY VideoStartDt ASC
		LIMIT ?
	`)
	if err != nil {
		return err
	}

	stmtAfterGame, err = db.Prepare(`
		SELECT RoundCode, GameTypeId, GameType, RaceNumber, RaceDate, Status,
			CompetitorsCount, CompetitorsJson, OddsJson, WinOddsJson, Bonus,
			VideoName, COALESCE(VideoNameJson,''), Weather, Temperature, Humidity, Wind, CourseConditions,
			IntervalJson, COALESCE(JackpotInfoJson,''), VideoStartDt, VideoEndDt, RoundInterval, ScheduledAt,
			CreatedAt, FinishedAt
		FROM GameRounds
		WHERE GameTypeId=? AND VideoStartDt > (
			SELECT VideoStartDt FROM GameRounds WHERE RoundCode=?
		)
		ORDER BY VideoStartDt ASC
		LIMIT ?
	`)
	if err != nil {
		return err
	}

	stmtStats, err = db.Prepare(`
		SELECT GameType,
			COUNT(*) AS total,
			SUM(CASE WHEN Status='F' THEN 1 ELSE 0 END) AS finished
		FROM GameRounds
		GROUP BY GameType
	`)
	if err != nil {
		return err
	}

	// StatsHistory: last N finished round codes for a betoffer, ordered newest first.
	stmtStatsHist, err = db.Prepare(`
		SELECT RoundCode
		FROM GameRounds
		WHERE GameTypeId=? AND Status='F'
		ORDER BY VideoStartDt DESC
		LIMIT ?
	`)
	if err != nil {
		return err
	}

	// PatchFinished: update interval/videoname/jackpot on finished games that are missing them.
	stmtPatchFinished, err = db.Prepare(`
		UPDATE GameRounds
		SET IntervalJson=CASE WHEN ?!='' THEN ? ELSE IntervalJson END,
		    VideoNameJson=CASE WHEN ?!='' THEN ? ELSE VideoNameJson END,
		    VideoName=CASE WHEN ?!='' THEN ? ELSE VideoName END,
		    JackpotInfoJson=CASE WHEN ?!='' THEN ? ELSE JackpotInfoJson END,
		    CompetitorsJson=CASE WHEN ?!='' AND ?!='[]' THEN ? ELSE CompetitorsJson END
		WHERE RoundCode=?
	`)
	if err != nil {
		return err
	}

	return nil
}

// Close closes the database connection and all prepared statements.
func Close() error {
	stmts := []*sql.Stmt{
		stmtUpsert, stmtFinish, stmtInsertResult,
		stmtHistory, stmtFuture, stmtAfterGame, stmtStats, stmtStatsHist, stmtPatchFinished,
	}
	for _, s := range stmts {
		if s != nil {
			s.Close()
		}
	}
	if dbRO != nil {
		_ = dbRO.Close()
		dbRO = nil
	}
	if db != nil {
		return db.Close()
	}
	return nil
}

// OddsHash computes a fast MD5 hash of odds to detect changes.
// It checks the first 3 and last element of the odds JSON array.
func OddsHash(oddsJSON string) string {
	if oddsJSON == "" || oddsJSON == "[]" || oddsJSON == "null" {
		return ""
	}

	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(oddsJSON), &arr); err != nil || len(arr) == 0 {
		return ""
	}

	var parts []string
	for i := 0; i < 3 && i < len(arr); i++ {
		parts = append(parts, string(arr[i]))
	}
	if len(arr) > 3 {
		parts = append(parts, string(arr[len(arr)-1]))
	}

	h := md5.Sum([]byte(strings.Join(parts, "|"))) // #nosec G401 -- non-cryptographic dedup hash, not for security.
	return fmt.Sprintf("%x", h)
}

// UpsertGameRound inserts or updates a game round. Returns true if actually written (not skipped).
// For finished games: only patches interval/videoname/competitors if missing.
// For open games: full upsert, preserving bonus.
func UpsertGameRound(gr *models.GameRound) (written bool, err error) {
	// Check existing game state.
	var existingStatus sql.NullString
	var existingOdds sql.NullString
	var existingBonus sql.NullInt64
	row := db.QueryRow("SELECT Status, OddsJson, Bonus FROM GameRounds WHERE RoundCode=?", gr.RoundCode)
	if err := row.Scan(&existingStatus, &existingOdds, &existingBonus); err == nil {
		// Finished game: don't overwrite, but patch missing fields and bonus.
		if existingStatus.Valid && existingStatus.String == "F" {
			if gr.IntervalJSON != "" || gr.VideoNameJSON != "" || gr.JackpotInfoJSON != "" {
				_, _ = stmtPatchFinished.Exec(
					gr.IntervalJSON, gr.IntervalJSON,
					gr.VideoNameJSON, gr.VideoNameJSON,
					gr.VideoName, gr.VideoName,
					gr.JackpotInfoJSON, gr.JackpotInfoJSON,
					gr.CompetitorsJSON, gr.CompetitorsJSON, gr.CompetitorsJSON,
					gr.RoundCode,
				)
			}
			if gr.Bonus > 0 && (!existingBonus.Valid || existingBonus.Int64 == 0) {
				_, _ = db.Exec("UPDATE GameRounds SET Bonus=? WHERE RoundCode=?", gr.Bonus, gr.RoundCode)
			}
			return false, nil
		}
		// Preserve bonus: always keep the higher value (vendor clears to 0 post-race).
		if existingBonus.Valid && existingBonus.Int64 > int64(gr.Bonus) {
			gr.Bonus = int(existingBonus.Int64)
		}
		// Update bonus in DB even if odds haven't changed.
		if gr.Bonus > 0 && (!existingBonus.Valid || existingBonus.Int64 == 0) {
			_, execErr := db.Exec("UPDATE GameRounds SET Bonus=? WHERE RoundCode=?", gr.Bonus, gr.RoundCode)
			if execErr == nil {
				log.Printf("[SQLITE] bonus updated %s → %d", gr.RoundCode, gr.Bonus)
			}
		}
		// Skip full upsert if odds haven't changed.
		if existingOdds.Valid && OddsHash(existingOdds.String) == OddsHash(gr.OddsJSON) {
			return false, nil
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if gr.CreatedAt == "" {
		gr.CreatedAt = now
	}

	_, err = stmtUpsert.Exec(
		gr.RoundCode, gr.GameTypeID, gr.GameType, gr.RaceNumber, gr.RaceDate, gr.Status,
		gr.CompetitorsCount, gr.CompetitorsJSON, gr.OddsJSON, gr.WinOddsJSON, gr.Bonus,
		gr.VideoName, gr.VideoNameJSON, gr.Weather, gr.Temperature, gr.Humidity, gr.Wind, gr.CourseConditions,
		gr.IntervalJSON, gr.JackpotInfoJSON, gr.VideoStartDt, gr.VideoEndDt, gr.RoundInterval, gr.ScheduledAt,
		gr.CreatedAt, gr.FinishedAt,
	)
	if err != nil {
		return false, fmt.Errorf("upsert game round %s: %w", gr.RoundCode, err)
	}
	return true, nil
}

// UpsertGameRoundBatch wraps multiple upserts in a single transaction.
// Returns count of actually written rows.
func UpsertGameRoundBatch(rounds []*models.GameRound) (written int, err error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	txUpsert := tx.Stmt(stmtUpsert)
	defer txUpsert.Close()

	for _, gr := range rounds {
		// Check existing game state within the transaction.
		var existingStatus sql.NullString
		var existingOdds sql.NullString
		var existingBonus sql.NullInt64
		row := tx.QueryRow("SELECT Status, OddsJson, Bonus FROM GameRounds WHERE RoundCode=?", gr.RoundCode)
		if scanErr := row.Scan(&existingStatus, &existingOdds, &existingBonus); scanErr == nil {
			// Finished game: patch missing fields and bonus.
			if existingStatus.Valid && existingStatus.String == "F" {
				if gr.IntervalJSON != "" || gr.VideoNameJSON != "" || gr.JackpotInfoJSON != "" {
					patchStmt := tx.Stmt(stmtPatchFinished)
					_, _ = patchStmt.Exec(
						gr.IntervalJSON, gr.IntervalJSON,
						gr.VideoNameJSON, gr.VideoNameJSON,
						gr.VideoName, gr.VideoName,
						gr.JackpotInfoJSON, gr.JackpotInfoJSON,
						gr.CompetitorsJSON, gr.CompetitorsJSON, gr.CompetitorsJSON,
						gr.RoundCode,
					)
					patchStmt.Close()
				}
				// Update bonus on finished games too (vendor sends bonus=1 after race).
				if gr.Bonus > 0 && (!existingBonus.Valid || existingBonus.Int64 == 0) {
					_, _ = tx.Exec("UPDATE GameRounds SET Bonus=? WHERE RoundCode=?", gr.Bonus, gr.RoundCode)
				}
				continue
			}
			// Preserve bonus: always keep the higher value.
			if existingBonus.Valid && existingBonus.Int64 > int64(gr.Bonus) {
				gr.Bonus = int(existingBonus.Int64)
			}
			// Update bonus even if odds unchanged.
			if gr.Bonus > 0 && (!existingBonus.Valid || existingBonus.Int64 == 0) {
				_, _ = tx.Exec("UPDATE GameRounds SET Bonus=? WHERE RoundCode=?", gr.Bonus, gr.RoundCode)
				log.Printf("[SQLITE] batch bonus updated %s → %d", gr.RoundCode, gr.Bonus)
			}
			// Skip full upsert if odds unchanged.
			if existingOdds.Valid && OddsHash(existingOdds.String) == OddsHash(gr.OddsJSON) {
				continue
			}
		}

		now := time.Now().UTC().Format(time.RFC3339)
		if gr.CreatedAt == "" {
			gr.CreatedAt = now
		}

		_, execErr := txUpsert.Exec(
			gr.RoundCode, gr.GameTypeID, gr.GameType, gr.RaceNumber, gr.RaceDate, gr.Status,
			gr.CompetitorsCount, gr.CompetitorsJSON, gr.OddsJSON, gr.WinOddsJSON, gr.Bonus,
			gr.VideoName, gr.VideoNameJSON, gr.Weather, gr.Temperature, gr.Humidity, gr.Wind, gr.CourseConditions,
			gr.IntervalJSON, gr.JackpotInfoJSON, gr.VideoStartDt, gr.VideoEndDt, gr.RoundInterval, gr.ScheduledAt,
			gr.CreatedAt, gr.FinishedAt,
		)
		if execErr != nil {
			err = fmt.Errorf("upsert game round %s: %w", gr.RoundCode, execErr)
			return 0, err
		}
		written++
	}

	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}
	return written, nil
}

// SaveResult marks a game as finished and inserts result positions.
func SaveResult(roundCode string, results []models.GameResult) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	now := time.Now().UTC().Format(time.RFC3339)
	txFinish := tx.Stmt(stmtFinish)
	defer txFinish.Close()

	if _, err = txFinish.Exec(now, roundCode); err != nil {
		return fmt.Errorf("finish round %s: %w", roundCode, err)
	}

	txInsert := tx.Stmt(stmtInsertResult)
	defer txInsert.Close()

	for _, r := range results {
		if _, err = txInsert.Exec(roundCode, r.Position, r.RunnerNumber, r.FinishTime); err != nil {
			return fmt.Errorf("insert result for %s pos %d: %w", roundCode, r.Position, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit result tx: %w", err)
	}
	return nil
}

// scanGameRound scans a single GameRound from a row scanner.
func scanGameRound(scanner interface {
	Scan(dest ...interface{}) error
}) (*models.GameRound, error) {
	gr := &models.GameRound{}
	err := scanner.Scan(
		&gr.RoundCode, &gr.GameTypeID, &gr.GameType, &gr.RaceNumber, &gr.RaceDate, &gr.Status,
		&gr.CompetitorsCount, &gr.CompetitorsJSON, &gr.OddsJSON, &gr.WinOddsJSON, &gr.Bonus,
		&gr.VideoName, &gr.VideoNameJSON, &gr.Weather, &gr.Temperature, &gr.Humidity, &gr.Wind, &gr.CourseConditions,
		&gr.IntervalJSON, &gr.JackpotInfoJSON, &gr.VideoStartDt, &gr.VideoEndDt, &gr.RoundInterval, &gr.ScheduledAt,
		&gr.CreatedAt, &gr.FinishedAt,
	)
	if err != nil {
		return nil, err
	}
	return gr, nil
}

// scanGameRounds scans multiple GameRounds from sql.Rows.
func scanGameRounds(rows *sql.Rows) ([]*models.GameRound, error) {
	var result []*models.GameRound
	for rows.Next() {
		gr, err := scanGameRound(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, gr)
	}
	return result, rows.Err()
}

// GamesWindow returns history (finished) + future (open) games for a betoffer.
// Results are sorted by VideoStartDt ASC.
func GamesWindow(betofferID int, historyCount int, futureCount int) ([]*models.GameRound, error) {
	// Fetch finished games (most recent first, then we reverse).
	historyRows, err := stmtHistory.Query(betofferID, abs(historyCount))
	if err != nil {
		return nil, fmt.Errorf("query history: %w", err)
	}
	defer historyRows.Close()

	history, err := scanGameRounds(historyRows)
	if err != nil {
		return nil, fmt.Errorf("scan history: %w", err)
	}

	// Reverse history so it's ASC by VideoStartDt.
	for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
		history[i], history[j] = history[j], history[i]
	}

	// Fetch future/open games.
	futureRows, err := stmtFuture.Query(betofferID, futureCount)
	if err != nil {
		return nil, fmt.Errorf("query future: %w", err)
	}
	defer futureRows.Close()

	future, err := scanGameRounds(futureRows)
	if err != nil {
		return nil, fmt.Errorf("scan future: %w", err)
	}

	// Combine: history (ASC) + future (ASC).
	result := make([]*models.GameRound, 0, len(history)+len(future))
	result = append(result, history...)
	result = append(result, future...)
	return result, nil
}

// GamesAfterGameID returns games after a specific game ID (for incremental sync).
func GamesAfterGameID(betofferID int, gameID string, limit int) ([]*models.GameRound, error) {
	rows, err := stmtAfterGame.Query(betofferID, gameID, limit)
	if err != nil {
		return nil, fmt.Errorf("query after game %s: %w", gameID, err)
	}
	defer rows.Close()

	return scanGameRounds(rows)
}

// ResultsBatch returns results for multiple round codes at once.
func ResultsBatch(roundCodes []string) (map[string][]models.GameResult, error) {
	if len(roundCodes) == 0 {
		return make(map[string][]models.GameResult), nil
	}

	placeholders := make([]string, len(roundCodes))
	args := make([]interface{}, len(roundCodes))
	for i, rc := range roundCodes {
		placeholders[i] = "?"
		args[i] = rc
	}

	// #nosec G201 -- placeholders is a generated "?,?,..." list; values are passed via args.
	query := fmt.Sprintf(
		"SELECT GameRoundId, Position, RunnerNumber, FinishTime FROM GameResults WHERE GameRoundId IN (%s) ORDER BY GameRoundId, Position",
		strings.Join(placeholders, ","),
	)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query results batch: %w", err)
	}
	defer rows.Close()

	results := make(map[string][]models.GameResult)
	for rows.Next() {
		var r models.GameResult
		if err := rows.Scan(&r.GameRoundID, &r.Position, &r.RunnerNumber, &r.FinishTime); err != nil {
			return nil, fmt.Errorf("scan result: %w", err)
		}
		results[r.GameRoundID] = append(results[r.GameRoundID], r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// ResultsExist returns true if results have been written for the given round.
// Used by the guardian to check if Channel A already wrote a result.
func ResultsExist(roundCode string) bool {
	var count int
	db.QueryRow("SELECT COUNT(*) FROM GameResults WHERE GameRoundId = ?", roundCode).Scan(&count)
	return count > 0
}

// ResultsByRoundCode returns the results for a single round (read-only).
// Used by the guardian to compare Channel A's data with Channel C's.
func ResultsByRoundCode(roundCode string) ([]models.GameResult, error) {
	rows, err := db.Query(
		"SELECT GameRoundId, Position, RunnerNumber, FinishTime FROM GameResults WHERE GameRoundId = ? ORDER BY Position",
		roundCode,
	)
	if err != nil {
		return nil, fmt.Errorf("query results: %w", err)
	}
	defer rows.Close()

	var results []models.GameResult
	for rows.Next() {
		var r models.GameResult
		if err := rows.Scan(&r.GameRoundID, &r.Position, &r.RunnerNumber, &r.FinishTime); err != nil {
			return nil, fmt.Errorf("scan result: %w", err)
		}
		results = append(results, r)
	}
	return results, nil
}

// Stats returns total and finished counts per game type.
func Stats() ([]models.GameTypeStats, error) {
	rows, err := stmtStats.Query()
	if err != nil {
		return nil, fmt.Errorf("query stats: %w", err)
	}
	defer rows.Close()

	var stats []models.GameTypeStats
	for rows.Next() {
		var s models.GameTypeStats
		if err := rows.Scan(&s.GameType, &s.Total, &s.Finished); err != nil {
			return nil, fmt.Errorf("scan stats: %w", err)
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}

// StatsHistory returns the last N finished races for a betoffer as arrays of finish positions.
// Each entry is [1st, 2nd, 3rd, ...] runner numbers. Also returns newest/oldest round codes.
func StatsHistory(betofferID int, limit int) (history [][]int, newest string, oldest string, err error) {
	rows, err := stmtStatsHist.Query(betofferID, limit)
	if err != nil {
		return nil, "", "", fmt.Errorf("query stats history: %w", err)
	}
	defer rows.Close()

	var roundCodes []string
	for rows.Next() {
		var rc string
		if err := rows.Scan(&rc); err != nil {
			return nil, "", "", fmt.Errorf("scan stats history: %w", err)
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

	resultMap, err := ResultsBatch(roundCodes)
	if err != nil {
		return nil, "", "", fmt.Errorf("results for stats: %w", err)
	}

	// Build history arrays in order (newest first).
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

// PatchFinishedGame updates interval, videoname and jackpotInfo on a finished game
// that is missing them. Only updates if the existing IntervalJson is empty.
func PatchFinishedGame(roundCode, intervalJSON, videoNameJSON, jackpotInfoJSON string) {
	if stmtPatchFinished == nil {
		return
	}
	// Extract base video name from videoNameJSON.
	videoName := ""
	if videoNameJSON != "" {
		var vnObj map[string]string
		if err := json.Unmarshal([]byte(videoNameJSON), &vnObj); err == nil {
			if mp4, ok := vnObj["mp4"]; ok {
				videoName = extractBaseName(mp4)
			}
		}
	}
	_, _ = stmtPatchFinished.Exec(
		intervalJSON, intervalJSON,
		videoNameJSON, videoNameJSON,
		videoName, videoName,
		jackpotInfoJSON, jackpotInfoJSON,
		"", "", "",
		roundCode,
	)
}

// GamesByRoundCodes returns game rounds for specific round codes that exist in SQLite.
// Codes that don't exist are silently skipped.
func GamesByRoundCodes(codes []string) ([]*models.GameRound, error) {
	if len(codes) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(codes))
	args := make([]interface{}, len(codes))
	for i, c := range codes {
		placeholders[i] = "?"
		args[i] = c
	}

	// #nosec G201 -- placeholders is a generated "?,?,..." list; values are passed via args.
	query := fmt.Sprintf(`
		SELECT RoundCode, GameTypeId, GameType, RaceNumber, RaceDate, Status,
			CompetitorsCount, CompetitorsJson, OddsJson, WinOddsJson, Bonus,
			VideoName, COALESCE(VideoNameJson,''), Weather, Temperature, Humidity, Wind, CourseConditions,
			IntervalJson, COALESCE(JackpotInfoJson,''), VideoStartDt, VideoEndDt, RoundInterval, ScheduledAt,
			CreatedAt, FinishedAt
		FROM GameRounds
		WHERE RoundCode IN (%s)
		ORDER BY VideoStartDt ASC
	`, strings.Join(placeholders, ","))

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query by round codes: %w", err)
	}
	defer rows.Close()
	return scanGameRounds(rows)
}

// GameByRoundCode returns a single game round by its round code.
func GameByRoundCode(roundCode string) (*models.GameRound, error) {
	row := db.QueryRow(`
		SELECT RoundCode, GameTypeId, GameType, RaceNumber, RaceDate, Status,
			CompetitorsCount, CompetitorsJson, OddsJson, WinOddsJson, Bonus,
			VideoName, COALESCE(VideoNameJson,''), Weather, Temperature, Humidity, Wind, CourseConditions,
			IntervalJson, COALESCE(JackpotInfoJson,''), VideoStartDt, VideoEndDt, RoundInterval, ScheduledAt,
			CreatedAt, FinishedAt
		FROM GameRounds WHERE RoundCode=?
	`, roundCode)
	return scanGameRound(row)
}

// RecentResults returns the last N finished games for a betoffer, with results.
func RecentResults(betofferID int, limit int) ([]*models.GameRound, map[string][]models.GameResult, error) {
	rows, err := stmtHistory.Query(betofferID, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("query recent results: %w", err)
	}
	defer rows.Close()

	games, err := scanGameRounds(rows)
	if err != nil {
		return nil, nil, fmt.Errorf("scan recent results: %w", err)
	}

	codes := make([]string, len(games))
	for i, g := range games {
		codes[i] = g.RoundCode
	}

	resultMap, err := ResultsBatch(codes)
	if err != nil {
		return nil, nil, err
	}
	return games, resultMap, nil
}

// UpcomingGames returns the next N open games for a betoffer.
func UpcomingGames(betofferID int, limit int) ([]*models.GameRound, error) {
	rows, err := stmtFuture.Query(betofferID, limit)
	if err != nil {
		return nil, fmt.Errorf("query upcoming: %w", err)
	}
	defer rows.Close()
	return scanGameRounds(rows)
}

// CurrentGame returns the first open game for a betoffer (the next race).
func CurrentGame(betofferID int) (*models.GameRound, error) {
	games, err := UpcomingGames(betofferID, 1)
	if err != nil {
		return nil, err
	}
	if len(games) == 0 {
		return nil, fmt.Errorf("no upcoming games for betoffer %d", betofferID)
	}
	return games[0], nil
}

// extractBaseName extracts "R0163" from URLs like "https://.../dog6/R0163_h.mp4?Expires=..."
func extractBaseName(mp4Path string) string {
	if idx := strings.Index(mp4Path, "?"); idx >= 0 {
		mp4Path = mp4Path[:idx]
	}
	if idx := strings.LastIndex(mp4Path, "/"); idx >= 0 {
		mp4Path = mp4Path[idx+1:]
	}
	if idx := strings.LastIndex(mp4Path, "."); idx >= 0 {
		mp4Path = mp4Path[:idx]
	}
	for i, c := range mp4Path {
		if i > 0 && c == '_' {
			return mp4Path[:i]
		}
	}
	return mp4Path
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
