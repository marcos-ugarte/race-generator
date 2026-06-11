// sqlite_test.go — unit tests for InitReadOnly (sub-phase 2.J / audit
// finding C1). These exercise the read-only handle against a real
// on-disk SQLite file, verifying that:
//
//   - the handle opens cleanly when the file already exists
//   - INSERT / UPDATE / DELETE through the read-only handle errors out
//
// We do NOT test the rest of the package here; that lives in higher
// levels of the existing test suite.
package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	_ "modernc.org/sqlite"
)

// resetForTest tears down all package state between tests so InitReadOnly
// (and the writable Init) start from a known position.
func resetForTest(t *testing.T) {
	t.Helper()
	_ = Close()
	db = nil
	dbRO = nil
	stmtUpsert, stmtFinish, stmtInsertResult = nil, nil, nil
	stmtHistory, stmtFuture, stmtAfterGame = nil, nil, nil
	stmtStats, stmtStatsHist, stmtPatchFinished = nil, nil, nil
}

func TestInitReadOnly_ReadsExistingFile_Succeeds(t *testing.T) {
	resetForTest(t)
	path := filepath.Join(t.TempDir(), "ro.db")

	// Create the file with a writable handle first (mode=ro can't
	// create the file).
	if err := Init(path); err != nil {
		t.Fatalf("Init(path) failed: %v", err)
	}
	defer resetForTest(t)

	if err := InitReadOnly(path); err != nil {
		t.Fatalf("InitReadOnly: %v", err)
	}
	if DBReadOnly() == nil {
		t.Fatal("DBReadOnly() returned nil after successful InitReadOnly")
	}
	// Smoke read.
	rows, err := DBReadOnly().Query("SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		t.Fatalf("read query: %v", err)
	}
	rows.Close()
}

func TestInitReadOnly_RejectWrites(t *testing.T) {
	resetForTest(t)
	path := filepath.Join(t.TempDir(), "ro_writes.db")

	if err := Init(path); err != nil {
		t.Fatalf("Init(path): %v", err)
	}
	defer resetForTest(t)

	// Seed one row through the writable handle so UPDATE/DELETE have
	// something to attempt against.
	if _, err := DB().Exec(
		`INSERT INTO GameRounds (RoundCode, GameTypeId, GameType) VALUES (?,?,?)`,
		"test-round", 100, "horse",
	); err != nil {
		t.Fatalf("seed insert: %v", err)
	}

	if err := InitReadOnly(path); err != nil {
		t.Fatalf("InitReadOnly: %v", err)
	}

	cases := []struct {
		name string
		sql  string
	}{
		{"INSERT", `INSERT INTO GameRounds (RoundCode) VALUES ('attacker')`},
		{"UPDATE", `UPDATE GameRounds SET GameType='hacked' WHERE RoundCode='test-round'`},
		{"DELETE", `DELETE FROM GameRounds WHERE RoundCode='test-round'`},
		{"CREATE", `CREATE TABLE evil (id INT)`},
		{"DROP", `DROP TABLE GameRounds`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DBReadOnly().Exec(tc.sql)
			if err == nil {
				t.Fatalf("write %q succeeded through RO handle — must error", tc.sql)
			}
		})
	}

	// Sanity: the writable handle still works (we did not break Init).
	if _, err := DB().Exec(`UPDATE GameRounds SET GameType='still-writable' WHERE RoundCode='test-round'`); err != nil {
		t.Fatalf("writable handle broken: %v", err)
	}
}

// TestInit_DedupeAndUniqueIndex_OnExistingDuplicates: simulate the
// pre-2026-05-05 production state — a DB with duplicate (GameRoundId,
// Position) rows — and confirm Init() dedupes them, creates the
// UNIQUE INDEX, and that subsequent stmtInsertResult is a no-op on
// repeats.
// TestRequireExistingDBFile pins the opt-in fail-fast contract: with
// VIRTUALES_DB_REQUIRE_EXISTING=1 set, calling Init on a path that
// doesn't exist must return a clear error instead of creating an
// empty file (which would silently corrupt the post-cutover writer
// expectations). Default (env unset) preserves legacy create-on-missing.
func TestRequireExistingDBFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.db")

	// Default: env unset → Init creates the file (legacy behaviour).
	resetForTest(t)
	if err := Init(missing); err != nil {
		t.Fatalf("Init(missing) without env: unexpected error %v", err)
	}
	if _, err := os.Stat(missing); err != nil {
		t.Errorf("Init did not create the file in legacy mode: %v", err)
	}
	_ = Close()
	_ = os.Remove(missing)

	// Opt-in: VIRTUALES_DB_REQUIRE_EXISTING=1 → Init fails fast.
	resetForTest(t)
	t.Setenv("VIRTUALES_DB_REQUIRE_EXISTING", "1")
	err := Init(missing)
	if err == nil {
		t.Fatalf("Init(missing) with require=1: expected error, got nil")
	}
	if _, statErr := os.Stat(missing); statErr == nil {
		t.Errorf("Init created file despite fail-fast gating")
	}

	// With the env set AND the file already present, Init must
	// succeed normally (the gate only short-circuits the missing
	// case).
	resetForTest(t)
	present := filepath.Join(dir, "present.db")
	t.Setenv("VIRTUALES_DB_REQUIRE_EXISTING", "")
	// Bootstrap the file via legacy Init.
	if err := Init(present); err != nil {
		t.Fatalf("bootstrap Init(present): %v", err)
	}
	_ = Close()
	resetForTest(t)
	t.Setenv("VIRTUALES_DB_REQUIRE_EXISTING", "1")
	if err := Init(present); err != nil {
		t.Fatalf("Init(present) with require=1: unexpected error %v", err)
	}
}

func TestInit_DedupeAndUniqueIndex_OnExistingDuplicates(t *testing.T) {
	resetForTest(t)
	path := filepath.Join(t.TempDir(), "dedupe.db")

	// Bootstrap the DB with an EARLIER version of the schema (no unique
	// index) so we can plant duplicates the way the live DBs have them.
	if err := Init(path); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Drop the index that the new Init() just created so we can plant
	// duplicates and run Init() a second time below.
	if _, err := DB().Exec("DROP INDEX IF EXISTS uniq_game_results_round_pos"); err != nil {
		t.Fatalf("drop index: %v", err)
	}

	// Plant 5 distinct (round, pos) pairs, each one inserted twice.
	for i := 0; i < 5; i++ {
		for j := 0; j < 2; j++ {
			_, err := DB().Exec(
				`INSERT INTO GameResults (GameRoundId, Position, RunnerNumber, FinishTime) VALUES (?,?,?,?)`,
				"r", i+1, i+1, float64(i)+0.5,
			)
			if err != nil {
				t.Fatalf("seed dup: %v", err)
			}
		}
	}
	var pre int
	if err := DB().QueryRow("SELECT COUNT(*) FROM GameResults").Scan(&pre); err != nil {
		t.Fatalf("count pre: %v", err)
	}
	if pre != 10 {
		t.Fatalf("expected 10 seeded rows, got %d", pre)
	}

	// Re-init: Close + Init a second time on the same file so the dedupe
	// + index creation path runs against the planted duplicates.
	resetForTest(t)
	if err := Init(path); err != nil {
		t.Fatalf("Init second time: %v", err)
	}

	var post int
	if err := DB().QueryRow("SELECT COUNT(*) FROM GameResults").Scan(&post); err != nil {
		t.Fatalf("count post: %v", err)
	}
	if post != 5 {
		t.Fatalf("dedupe failed: post=%d want 5", post)
	}

	// Index must exist.
	var indexName string
	if err := DB().QueryRow(
		`SELECT name FROM sqlite_master WHERE type='index' AND name='uniq_game_results_round_pos'`,
	).Scan(&indexName); err != nil {
		t.Fatalf("unique index missing: %v", err)
	}

	// stmtInsertResult must now be silent on duplicate inserts.
	if _, err := stmtInsertResult.Exec("r", 1, 99, 1.23); err != nil {
		t.Fatalf("INSERT OR IGNORE on duplicate must succeed (no-op): %v", err)
	}
	var after int
	if err := DB().QueryRow("SELECT COUNT(*) FROM GameResults").Scan(&after); err != nil {
		t.Fatalf("count after dup-insert: %v", err)
	}
	if after != 5 {
		t.Fatalf("INSERT OR IGNORE inserted a duplicate row: count=%d", after)
	}

	// And a NEW (round, pos) still inserts normally.
	if _, err := stmtInsertResult.Exec("r", 99, 1, 0); err != nil {
		t.Fatalf("legitimate insert blocked: %v", err)
	}
	if err := DB().QueryRow("SELECT COUNT(*) FROM GameResults").Scan(&after); err != nil {
		t.Fatalf("count after fresh insert: %v", err)
	}
	if after != 6 {
		t.Fatalf("fresh insert did not land: count=%d want 6", after)
	}
	resetForTest(t)
}

// TestInit_DedupeIsIdempotent_OnFreshDB: running Init() against a
// brand-new DB must NOT error on the dedupe step or the unique index
// (both must be no-ops). This is the path every dev/testdb run takes.
func TestInit_DedupeIsIdempotent_OnFreshDB(t *testing.T) {
	resetForTest(t)
	path := filepath.Join(t.TempDir(), "fresh.db")
	if err := Init(path); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	resetForTest(t)
	// Second Init() of the same file: dedupe still finds no duplicates,
	// CREATE UNIQUE INDEX IF NOT EXISTS does nothing, prepare succeeds.
	if err := Init(path); err != nil {
		t.Fatalf("second Init: %v", err)
	}
	resetForTest(t)
}

// isBusyErr is a thin wrapper around the package-level isBusyError so
// the tests below keep their previous local name. Both must report the
// same thing — keep them in lock-step.
func isBusyErr(err error) bool { return isBusyError(err) }

// openBuggy replicates the EXACT pragma-ordering bug that lived in
// Init() before the busy_timeout-via-DSN fix: open a plain DSN, then
// Exec the pragmas in the buggy order (WAL first, busy_timeout last).
// This is the literal pre-fix code path, lifted out so the regression
// test can demonstrate the race independent of Init's other side
// effects (package-level db var, prepareStatements, dedupe, etc.).
func openBuggy(t *testing.T, path string) (*sql.DB, error) {
	t.Helper()
	d, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
	}
	for _, p := range pragmas {
		if _, err := d.Exec(p); err != nil {
			_ = d.Close()
			return nil, fmt.Errorf("pragma %q: %w", p, err)
		}
	}
	return d, nil
}

// TestInit_Concurrent_FreshDB pins down the pre-existing cold-start race
// that lived in Init() before the busy_timeout-via-DSN fix.
//
// Repro: PRAGMA journal_mode=WAL needs an EXCLUSIVE lock. When N
// processes race over a fresh DB file, all of them call sql.Open
// (cheap, lazy, no lock), then each Exec("PRAGMA journal_mode=WAL")
// concurrently. The first wins; the rest crash with SQLITE_BUSY
// because PRAGMA busy_timeout=5000 was applied AFTER the WAL pragma
// in the postfacto pragma loop (zero-retry window).
//
// This test replicates the buggy code path (NOT Init() itself —
// openBuggy() replays the exact pre-fix Exec ordering) so it stays
// stable as a regression spec independent of whatever Init() does.
// After the fix is applied to Init(), this test KEEPS passing: it
// asserts the bug is real on the buggy path, not that Init() is
// buggy.
func TestInit_Concurrent_FreshDB(t *testing.T) {
	const N = 5
	path := filepath.Join(t.TempDir(), "race.db")

	var wg sync.WaitGroup
	var busyCount int32
	conns := make([]*sql.DB, N)
	errs := make([]error, N)

	wg.Add(N)
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			<-start // release all goroutines simultaneously
			d, err := openBuggy(t, path)
			if err != nil {
				errs[i] = err
				if isBusyErr(err) {
					atomic.AddInt32(&busyCount, 1)
				}
				return
			}
			conns[i] = d
		}(i)
	}
	close(start)
	wg.Wait()

	// Defer cleanup of any successful conns.
	defer func() {
		for _, c := range conns {
			if c != nil {
				_ = c.Close()
			}
		}
	}()

	// The race lets exactly one goroutine win the WAL lock; the others
	// (N-1) should fail with SQLITE_BUSY because the postfacto
	// busy_timeout pragma never gets a chance to install. We assert
	// AT LEAST N-1 failed, which proves the bug is reproducible.
	got := int(atomic.LoadInt32(&busyCount))
	if got < N-1 {
		t.Fatalf("buggy code path failed to reproduce race: "+
			"got %d SQLITE_BUSY errors, want >= %d. errs=%v",
			got, N-1, errs)
	}
	t.Logf("reproduced: %d/%d goroutines hit SQLITE_BUSY on cold-start race", got, N)
}

// TestInit_Concurrent_FreshDB_RealInit verifies the FIX inside the
// REAL sqlite.Init() function. We cannot just call sqlite.Init() from
// N goroutines because Init() writes to package-level vars (db,
// stmtUpsert, etc.) and N goroutines stomping on that mask the
// connection-level race we are testing.
//
// Solution: re-invoke this test binary as N child processes. Each
// child has its OWN process address space (and thus its own
// package-level db var), but they all race over the SAME fresh DB
// file on disk — which is exactly the production scenario
// (race-generator + collector-classic + tv-broadcaster all booting
// against the same relay.db).
//
// Children dispatch via TEST_INIT_CONCURRENT_PATH env var: if set,
// the test process calls sqlite.Init(path) and exits with the
// returned status code (0 = ok, 1 = error). If unset, the test acts
// as the parent: spawns N children, waits for all, counts failures.
//
// Expected BEFORE the fix: ~N-1 children fail with SQLITE_BUSY
// (the WAL-lock race in the postfacto pragma loop).
// Expected AFTER the fix: 0 failures (busy_timeout applied via DSN
// inside conn.Open closes the race).
func TestInit_Concurrent_FreshDB_RealInit(t *testing.T) {
	// Child-process mode: just call Init() and signal via exit code.
	if path := os.Getenv("TEST_INIT_CONCURRENT_PATH"); path != "" {
		if err := Init(path); err != nil {
			fmt.Fprintf(os.Stderr, "child Init err: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	const N = 5
	path := filepath.Join(t.TempDir(), "race_real_init.db")

	// Locate this test binary so children can re-invoke it.
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	var wg sync.WaitGroup
	var busyCount int32
	var otherErrCount int32
	errMsgs := make([]string, N)

	wg.Add(N)
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			cmd := exec.Command(self,
				"-test.run", "^TestInit_Concurrent_FreshDB_RealInit$",
				"-test.count=1",
			)
			cmd.Env = append(os.Environ(), "TEST_INIT_CONCURRENT_PATH="+path)
			out, err := cmd.CombinedOutput()
			if err == nil {
				return // success
			}
			errMsgs[i] = string(out)
			if isBusyErr(fmt.Errorf("%s", out)) {
				atomic.AddInt32(&busyCount, 1)
			} else {
				atomic.AddInt32(&otherErrCount, 1)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	busy := int(atomic.LoadInt32(&busyCount))
	other := int(atomic.LoadInt32(&otherErrCount))
	if busy != 0 || other != 0 {
		t.Fatalf("sqlite.Init cold-start race: %d/%d busy, %d/%d other-errors. "+
			"Sample child output: %s",
			busy, N, other, N, firstNonEmpty(errMsgs))
	}
}

func firstNonEmpty(ss []string) string {
	for _, s := range ss {
		if s != "" {
			if len(s) > 500 {
				return s[:500] + "...(truncated)"
			}
			return s
		}
	}
	return "(no error output captured)"
}

// TestInitMobile_Concurrent_FreshDB is the InitMobile twin of
// TestInit_Concurrent_FreshDB_RealInit. The production scenario is
// tv-broadcaster + collector-ds-pos-mobile both calling InitMobile()
// against the same fresh ds-pos-mobile.db at boot. Before the fix,
// the WAL pragma raced exactly the same way as relay.db's Init did;
// see InitMobile's leading comment for the doctrinal reason.
//
// Re-invokes this test binary in child-process mode the same way as
// the parent test above. Each child runs InitMobile and exits with
// 0/1; the parent counts SQLITE_BUSY exits. After the fix, expected
// 0 failures across the N children.
func TestInitMobile_Concurrent_FreshDB(t *testing.T) {
	// Child-process mode.
	if path := os.Getenv("TEST_INITMOBILE_CONCURRENT_PATH"); path != "" {
		if err := InitMobile(path); err != nil {
			fmt.Fprintf(os.Stderr, "child InitMobile err: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	const N = 5
	path := filepath.Join(t.TempDir(), "race_real_init_mobile.db")

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	var wg sync.WaitGroup
	var busyCount int32
	var otherErrCount int32
	errMsgs := make([]string, N)

	wg.Add(N)
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			cmd := exec.Command(self,
				"-test.run", "^TestInitMobile_Concurrent_FreshDB$",
				"-test.count=1",
			)
			cmd.Env = append(os.Environ(), "TEST_INITMOBILE_CONCURRENT_PATH="+path)
			out, err := cmd.CombinedOutput()
			if err == nil {
				return // success
			}
			errMsgs[i] = string(out)
			if isBusyErr(fmt.Errorf("%s", out)) {
				atomic.AddInt32(&busyCount, 1)
			} else {
				atomic.AddInt32(&otherErrCount, 1)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	busy := int(atomic.LoadInt32(&busyCount))
	other := int(atomic.LoadInt32(&otherErrCount))
	if busy != 0 || other != 0 {
		t.Fatalf("sqlite.InitMobile cold-start race: %d/%d busy, %d/%d other-errors. "+
			"Sample child output: %s",
			busy, N, other, N, firstNonEmpty(errMsgs))
	}
}
