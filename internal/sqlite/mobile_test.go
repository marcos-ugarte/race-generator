package sqlite

import (
	"errors"
	"path/filepath"
	"testing"

	"vg-racegen/internal/models"
)

// resetMobileForTest closes the mobile handle so each test starts fresh.
func resetMobileForTest(t *testing.T) {
	t.Helper()
	_ = CloseMobile()
}

// seedMobileRound is a small helper that inserts a GameRound directly into
// the mobile DB via the writable handle InitMobile opened. Test-only.
func seedMobileRound(t *testing.T, gr *models.GameRound) {
	t.Helper()
	if dbMobile == nil {
		t.Fatal("seedMobileRound: dbMobile is nil — call InitMobile first")
	}
	_, err := dbMobile.Exec(`
		INSERT INTO GameRounds (
			RoundCode, GameTypeId, GameType, RaceNumber, RaceDate, Status,
			CompetitorsCount, CompetitorsJson, OddsJson, WinOddsJson, Bonus,
			VideoName, VideoNameJson, Weather, Temperature, Humidity, Wind, CourseConditions,
			IntervalJson, JackpotInfoJson, VideoStartDt, VideoEndDt, RoundInterval, ScheduledAt,
			CreatedAt, FinishedAt
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		gr.RoundCode, gr.GameTypeID, gr.GameType, gr.RaceNumber, gr.RaceDate, gr.Status,
		gr.CompetitorsCount, gr.CompetitorsJSON, gr.OddsJSON, gr.WinOddsJSON, gr.Bonus,
		gr.VideoName, gr.VideoNameJSON, gr.Weather, gr.Temperature, gr.Humidity, gr.Wind, gr.CourseConditions,
		gr.IntervalJSON, gr.JackpotInfoJSON, gr.VideoStartDt, gr.VideoEndDt, gr.RoundInterval, gr.ScheduledAt,
		gr.CreatedAt, gr.FinishedAt,
	)
	if err != nil {
		t.Fatalf("seed mobile round %s: %v", gr.RoundCode, err)
	}
}

// seedMobileResult inserts a single GameResults row.
func seedMobileResult(t *testing.T, roundCode string, pos, runner int, finishTime *float64) {
	t.Helper()
	if _, err := dbMobile.Exec(
		`INSERT OR IGNORE INTO GameResults (GameRoundId, Position, RunnerNumber, FinishTime)
		 VALUES (?,?,?,?)`,
		roundCode, pos, runner, finishTime,
	); err != nil {
		t.Fatalf("seed mobile result: %v", err)
	}
}

// TestInitMobile_CreatesSchema verifies InitMobile opens a fresh file and
// creates GameRounds + GameResults with the same column set as the relay
// schema.
func TestInitMobile_CreatesSchema(t *testing.T) {
	resetMobileForTest(t)
	path := filepath.Join(t.TempDir(), "mobile.db")

	if err := InitMobile(path); err != nil {
		t.Fatalf("InitMobile: %v", err)
	}
	defer resetMobileForTest(t)

	if DBMobile() == nil {
		t.Fatal("DBMobile() returned nil after successful InitMobile")
	}

	// Verify both tables exist.
	var name string
	if err := dbMobile.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='GameRounds'`,
	).Scan(&name); err != nil {
		t.Fatalf("GameRounds table missing: %v", err)
	}
	if err := dbMobile.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='GameResults'`,
	).Scan(&name); err != nil {
		t.Fatalf("GameResults table missing: %v", err)
	}

	// VideoNameJson migration applied (added via alterMigrations).
	if _, err := dbMobile.Exec(
		`INSERT INTO GameRounds (RoundCode, VideoNameJson, JackpotInfoJson) VALUES (?,?,?)`,
		"probe", "{}", "{}",
	); err != nil {
		t.Fatalf("VideoNameJson column missing: %v", err)
	}

	// Unique index on results applied.
	var idxName string
	if err := dbMobile.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='index' AND name='uniq_game_results_round_pos'`,
	).Scan(&idxName); err != nil {
		t.Fatalf("unique index missing: %v", err)
	}
}

// TestInitMobile_Idempotent confirms a second InitMobile on the same file
// doesn't error and works correctly. The mobile collector and tv-broadcaster
// both call InitMobile on the same file; opening twice must be safe.
func TestInitMobile_Idempotent(t *testing.T) {
	resetMobileForTest(t)
	path := filepath.Join(t.TempDir(), "mobile.db")

	if err := InitMobile(path); err != nil {
		t.Fatalf("first InitMobile: %v", err)
	}
	resetMobileForTest(t)
	if err := InitMobile(path); err != nil {
		t.Fatalf("second InitMobile: %v", err)
	}
	resetMobileForTest(t)
}

// TestMobileGameByRoundCode reads a single seeded row back.
func TestMobileGameByRoundCode(t *testing.T) {
	resetMobileForTest(t)
	path := filepath.Join(t.TempDir(), "mobile.db")
	if err := InitMobile(path); err != nil {
		t.Fatalf("InitMobile: %v", err)
	}
	defer resetMobileForTest(t)

	gr := &models.GameRound{
		RoundCode: "141_101_202605140100", GameTypeID: 141, GameType: "dog6",
		Status: "F", VideoName: "R0100", VideoNameJSON: `{"mp4":"u/m.mp4","jpg":"u/m.jpg"}`,
		OddsJSON: "[]", CompetitorsJSON: "[]", IntervalJSON: `{"1":{}}`,
		VideoStartDt: "2026-05-14 10:00:00", VideoEndDt: "2026-05-14 10:00:45",
		RoundInterval: 240, Bonus: 1, CreatedAt: "2026-05-14T10:00:00Z",
	}
	seedMobileRound(t, gr)

	got, err := MobileGameByRoundCode(gr.RoundCode)
	if err != nil {
		t.Fatalf("MobileGameByRoundCode: %v", err)
	}
	if got.RoundCode != gr.RoundCode || got.GameTypeID != 141 || got.VideoNameJSON != gr.VideoNameJSON {
		t.Fatalf("round mismatch: got=%+v", got)
	}
}

// TestMobileGamesByRoundCodes seeds several rows and confirms ordering by
// VideoStartDt ASC + that missing codes are silently omitted.
func TestMobileGamesByRoundCodes(t *testing.T) {
	resetMobileForTest(t)
	path := filepath.Join(t.TempDir(), "mobile.db")
	if err := InitMobile(path); err != nil {
		t.Fatalf("InitMobile: %v", err)
	}
	defer resetMobileForTest(t)

	codes := []string{
		"141_101_202605140100",
		"141_101_202605140101",
		"141_101_202605140102",
	}
	starts := []string{
		"2026-05-14 10:08:00",
		"2026-05-14 10:00:00", // earliest
		"2026-05-14 10:04:00",
	}
	for i, c := range codes {
		seedMobileRound(t, &models.GameRound{
			RoundCode: c, GameTypeID: 141, GameType: "dog6", Status: "F",
			OddsJSON: "[]", CompetitorsJSON: "[]",
			VideoStartDt: starts[i], VideoEndDt: starts[i],
			RoundInterval: 240, CreatedAt: "2026-05-14T10:00:00Z",
		})
	}

	// Query 2 existing + 1 missing.
	got, err := MobileGamesByRoundCodes([]string{
		codes[0], codes[1], "missing_code",
	})
	if err != nil {
		t.Fatalf("MobileGamesByRoundCodes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got)=%d, want 2", len(got))
	}
	// Result must be ASC by VideoStartDt: 10:00 then 10:08.
	if got[0].VideoStartDt != "2026-05-14 10:00:00" {
		t.Fatalf("got[0].VideoStartDt=%q, want oldest first", got[0].VideoStartDt)
	}
	if got[1].VideoStartDt != "2026-05-14 10:08:00" {
		t.Fatalf("got[1].VideoStartDt=%q, want newest second", got[1].VideoStartDt)
	}
}

// TestMobileResultsBatch checks results are grouped by round code.
func TestMobileResultsBatch(t *testing.T) {
	resetMobileForTest(t)
	path := filepath.Join(t.TempDir(), "mobile.db")
	if err := InitMobile(path); err != nil {
		t.Fatalf("InitMobile: %v", err)
	}
	defer resetMobileForTest(t)

	// Seed 2 rounds with 3 results each.
	for _, rc := range []string{"r1", "r2"} {
		for p := 1; p <= 3; p++ {
			ft := 1.0 + float64(p)*0.1
			seedMobileResult(t, rc, p, p, &ft)
		}
	}

	got, err := MobileResultsBatch([]string{"r1", "r2", "missing"})
	if err != nil {
		t.Fatalf("MobileResultsBatch: %v", err)
	}
	if len(got["r1"]) != 3 || len(got["r2"]) != 3 {
		t.Fatalf("expected 3 results per round, got r1=%d r2=%d", len(got["r1"]), len(got["r2"]))
	}
	if _, ok := got["missing"]; ok {
		t.Fatalf("expected 'missing' to be absent from map")
	}
	// Each round's results must be ordered by Position ASC.
	for _, rc := range []string{"r1", "r2"} {
		for i, r := range got[rc] {
			if r.Position != i+1 {
				t.Fatalf("%s: position[%d]=%d, want %d", rc, i, r.Position, i+1)
			}
		}
	}
}

// TestMobileGamesWindow seeds a mix of F/O rounds and verifies the result
// is history (ASC by start) then future (ASC by start).
func TestMobileGamesWindow(t *testing.T) {
	resetMobileForTest(t)
	path := filepath.Join(t.TempDir(), "mobile.db")
	if err := InitMobile(path); err != nil {
		t.Fatalf("InitMobile: %v", err)
	}
	defer resetMobileForTest(t)

	// 3 finished (earliest is oldest history) + 2 open (earliest is next).
	cases := []struct {
		rc, start, status string
	}{
		{"f1", "2026-05-14 09:00:00", "F"},
		{"f2", "2026-05-14 09:04:00", "F"},
		{"f3", "2026-05-14 09:08:00", "F"},
		{"o1", "2026-05-14 10:00:00", "O"},
		{"o2", "2026-05-14 10:04:00", "O"},
	}
	for _, c := range cases {
		seedMobileRound(t, &models.GameRound{
			RoundCode: c.rc, GameTypeID: 141, GameType: "dog6", Status: c.status,
			OddsJSON: "[]", CompetitorsJSON: "[]",
			VideoStartDt: c.start, VideoEndDt: c.start,
			RoundInterval: 240, CreatedAt: "2026-05-14T09:00:00Z",
		})
	}

	got, err := MobileGamesWindow(141, 2, 1)
	if err != nil {
		t.Fatalf("MobileGamesWindow: %v", err)
	}
	// history: last 2 finished newest-DESC then reversed → f2, f3
	// future: next 1 open → o1
	want := []string{"f2", "f3", "o1"}
	if len(got) != len(want) {
		t.Fatalf("len(got)=%d want %d", len(got), len(want))
	}
	for i, g := range got {
		if g.RoundCode != want[i] {
			t.Fatalf("got[%d].RoundCode=%q want %q", i, g.RoundCode, want[i])
		}
	}
}

// TestMobileStatsHistory verifies the [position]=runner array layout and the
// newest/oldest bookend round codes.
func TestMobileStatsHistory(t *testing.T) {
	resetMobileForTest(t)
	path := filepath.Join(t.TempDir(), "mobile.db")
	if err := InitMobile(path); err != nil {
		t.Fatalf("InitMobile: %v", err)
	}
	defer resetMobileForTest(t)

	// 2 finished rounds, each with 3 finishing positions.
	for i, rc := range []string{"r_old", "r_new"} {
		// VideoStartDt makes r_new the newest.
		start := "2026-05-14 09:00:00"
		if i == 1 {
			start = "2026-05-14 10:00:00"
		}
		seedMobileRound(t, &models.GameRound{
			RoundCode: rc, GameTypeID: 141, GameType: "dog6", Status: "F",
			OddsJSON: "[]", CompetitorsJSON: "[]",
			VideoStartDt: start, VideoEndDt: start,
			RoundInterval: 240, CreatedAt: "2026-05-14T09:00:00Z",
		})
	}
	// r_old finish: 1st=runner 2, 2nd=runner 5, 3rd=runner 1
	for p, runner := range []int{2, 5, 1} {
		seedMobileResult(t, "r_old", p+1, runner, nil)
	}
	// r_new finish: 1st=runner 6, 2nd=runner 2, 3rd=runner 3
	for p, runner := range []int{6, 2, 3} {
		seedMobileResult(t, "r_new", p+1, runner, nil)
	}

	hist, newest, oldest, err := MobileStatsHistory(141, 20)
	if err != nil {
		t.Fatalf("MobileStatsHistory: %v", err)
	}
	if newest != "r_new" || oldest != "r_old" {
		t.Fatalf("newest/oldest got=%q/%q want r_new/r_old", newest, oldest)
	}
	// History is newest-first.
	if len(hist) != 2 {
		t.Fatalf("len(hist)=%d want 2", len(hist))
	}
	wantNewest := []int{6, 2, 3}
	wantOldest := []int{2, 5, 1}
	for i, v := range wantNewest {
		if hist[0][i] != v {
			t.Fatalf("hist[0][%d]=%d want %d", i, hist[0][i], v)
		}
	}
	for i, v := range wantOldest {
		if hist[1][i] != v {
			t.Fatalf("hist[1][%d]=%d want %d", i, hist[1][i], v)
		}
	}
}

// TestMobileGameByRoundCode_NotFound returns an error / nil for a missing code.
func TestMobileGameByRoundCode_NotFound(t *testing.T) {
	resetMobileForTest(t)
	path := filepath.Join(t.TempDir(), "mobile.db")
	if err := InitMobile(path); err != nil {
		t.Fatalf("InitMobile: %v", err)
	}
	defer resetMobileForTest(t)

	_, err := MobileGameByRoundCode("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing round code, got nil")
	}
}

// TestMobileFunctions_RejectWhenNotInitialised confirms the helpers fail
// cleanly when InitMobile was never called. cmd/tv-broadcaster swallows
// the InitMobile error so the box still starts without the mobile file;
// the helpers must not panic in that mode.
func TestMobileFunctions_RejectWhenNotInitialised(t *testing.T) {
	resetMobileForTest(t)
	defer resetMobileForTest(t)

	if _, err := MobileGameByRoundCode("x"); err == nil {
		t.Fatal("MobileGameByRoundCode: expected error when uninitialised")
	}
	if _, err := MobileGamesByRoundCodes([]string{"x"}); err == nil {
		t.Fatal("MobileGamesByRoundCodes: expected error when uninitialised")
	}
	if _, err := MobileResultsBatch([]string{"x"}); err == nil {
		t.Fatal("MobileResultsBatch: expected error when uninitialised")
	}
	if _, err := MobileGamesWindow(141, 1, 1); err == nil {
		t.Fatal("MobileGamesWindow: expected error when uninitialised")
	}
	if _, _, _, err := MobileStatsHistory(141, 20); err == nil {
		t.Fatal("MobileStatsHistory: expected error when uninitialised")
	}
}

// TestMobileFunctions_ReturnErrMobileDBClosed confirms the sentinel error
// (not a fmt.Errorf one-off) is what callers get, so errors.Is checks work.
func TestMobileFunctions_ReturnErrMobileDBClosed(t *testing.T) {
	resetMobileForTest(t)
	defer resetMobileForTest(t)

	cases := []struct {
		name string
		fn   func() error
	}{
		{"MobileGameByRoundCode", func() error {
			_, err := MobileGameByRoundCode("x")
			return err
		}},
		{"MobileGamesByRoundCodes", func() error {
			_, err := MobileGamesByRoundCodes([]string{"x"})
			return err
		}},
		{"MobileResultsBatch", func() error {
			_, err := MobileResultsBatch([]string{"x"})
			return err
		}},
		{"MobileGamesWindow", func() error {
			_, err := MobileGamesWindow(141, 1, 1)
			return err
		}},
		{"MobileStatsHistory", func() error {
			_, _, _, err := MobileStatsHistory(141, 20)
			return err
		}},
	}
	for _, c := range cases {
		err := c.fn()
		if !errors.Is(err, ErrMobileDBClosed) {
			t.Errorf("%s: got err=%v, want errors.Is(ErrMobileDBClosed)", c.name, err)
		}
	}
}
