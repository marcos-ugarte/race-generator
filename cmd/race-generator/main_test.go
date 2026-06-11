package main

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"vg-racegen/internal/racegen/audit"
	"vg-racegen/internal/racegen/config"
	"vg-racegen/internal/racegen/generators"
	"vg-racegen/internal/racegen/rng"
	"vg-racegen/internal/raceutil"
	"vg-racegen/internal/sqlite"
)

// TestNameCooldown_FIFOEviction verifies that the cooldown ring evicts the
// oldest entry when capacity is exceeded, NOT the entire ring like the
// previous wholesale-wipe strategy.
func TestNameCooldown_FIFOEviction(t *testing.T) {
	c := newNameCooldown(3)
	c.Add("a")
	c.Add("b")
	c.Add("c")
	if got := c.Len(); got != 3 {
		t.Fatalf("Len after 3 adds = %d, want 3", got)
	}
	// Adding the 4th should evict "a".
	c.Add("d")
	ex := c.Excludes()
	if ex["a"] {
		t.Errorf("expected oldest 'a' to be evicted, but it's still present")
	}
	for _, name := range []string{"b", "c", "d"} {
		if !ex[name] {
			t.Errorf("expected %q to be present after eviction", name)
		}
	}
	if got := c.Len(); got != 3 {
		t.Fatalf("Len after 4 adds (cap=3) = %d, want 3", got)
	}
}

// TestNameCooldown_DuplicateAddIsNoop verifies that re-adding a name does
// not double-count it or move it in the FIFO order — the legacy semantics
// keep a name cool for N rounds from its first sighting.
func TestNameCooldown_DuplicateAddIsNoop(t *testing.T) {
	c := newNameCooldown(3)
	c.Add("a")
	c.Add("b")
	c.Add("a") // duplicate — should NOT push "a" to the front
	c.Add("c")
	if got := c.Len(); got != 3 {
		t.Fatalf("Len = %d, want 3 (dup should be noop)", got)
	}
	// Adding "d" should evict "a" (oldest), not "b".
	c.Add("d")
	ex := c.Excludes()
	if ex["a"] {
		t.Errorf("expected oldest 'a' to be evicted, got present")
	}
	if !ex["b"] {
		t.Errorf("expected 'b' to still be present")
	}
}

// TestNameCooldown_TenRoundWindow simulates 12 rounds of 8 competitors
// each and asserts that competitors from round 11 (the freshest) are NOT
// among the names of rounds 1..10 (the cooldown window of 10 rounds), but
// MAY appear in round 1 (which has aged out by round 12).
//
// This is the regression test for the FIFO ring vs the old wholesale-wipe
// behavior: with the old wipe, after the threshold tripped, ALL recently-
// emitted names became eligible at once and round-11 had high collision
// probability with rounds 1..10.
func TestNameCooldown_TenRoundWindow(t *testing.T) {
	const numCompetitor = 8
	const capacity = numCompetitor * 10
	c := newNameCooldown(capacity)

	// Simulate 12 rounds. Each round emits 8 unique names. We use deterministic
	// distinct strings to be independent of the real RNG.
	rounds := make([][]string, 12)
	for round := 0; round < 12; round++ {
		names := make([]string, 0, numCompetitor)
		for i := 0; i < numCompetitor; i++ {
			names = append(names, generateTestName(round, i))
		}
		rounds[round] = names
		for _, n := range names {
			c.Add(n)
		}
	}

	// After 12 rounds at capacity=80, the cooldown contains rounds 3..12
	// (the last 10 rounds = 80 names). Rounds 1 and 2 should have been
	// evicted FIFO-style.
	if c.Len() != capacity {
		t.Fatalf("Len = %d, want %d (saturated FIFO at capacity)", c.Len(), capacity)
	}
	ex := c.Excludes()
	// All names from rounds 3..12 should be present.
	for round := 2; round < 12; round++ {
		for _, n := range rounds[round] {
			if !ex[n] {
				t.Errorf("round %d name %q should still be in cooldown, missing", round, n)
			}
		}
	}
	// All names from rounds 0..1 should have been evicted.
	for round := 0; round < 2; round++ {
		for _, n := range rounds[round] {
			if ex[n] {
				t.Errorf("round %d name %q should have been evicted FIFO, still present", round, n)
			}
		}
	}
}

// TestNameCooldown_FIFOOrderAfterRollover verifies that eviction order
// matches insertion order (FIFO): the oldest name is the first to become
// eligible again.
func TestNameCooldown_FIFOOrderAfterRollover(t *testing.T) {
	c := newNameCooldown(4)
	// Fill the ring to capacity.
	for _, n := range []string{"alpha", "bravo", "charlie", "delta"} {
		c.Add(n)
	}
	// Add 4 more — should evict alpha→bravo→charlie→delta in order.
	evictionOrder := []string{"alpha", "bravo", "charlie", "delta"}
	for i, newName := range []string{"echo", "foxtrot", "golf", "hotel"} {
		c.Add(newName)
		ex := c.Excludes()
		// Everything in evictionOrder[..i] should be gone.
		for j := 0; j <= i; j++ {
			if ex[evictionOrder[j]] {
				t.Errorf("after add %q, expected %q evicted, still present",
					newName, evictionOrder[j])
			}
		}
		// Everything in evictionOrder[i+1..] should still be present.
		for j := i + 1; j < len(evictionOrder); j++ {
			if !ex[evictionOrder[j]] {
				t.Errorf("after add %q, expected %q still present, missing",
					newName, evictionOrder[j])
			}
		}
	}
}

// generateTestName produces a deterministic unique competitor-style name
// for (round, slot). Pure helper for the cooldown tests — does not exercise
// the real name pool.
func generateTestName(round, slot int) string {
	return "R" + itoa(round) + "_S" + itoa(slot)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestTickRunner_RecoversFromPanic asserts that a panic inside the
// per-runner pipeline does NOT propagate out of tickRunner. The function
// must log + advance lastSlot + return so the scheduler can process the
// next runner / next tick. Without `defer recover()`, the whole binary
// would crash.
//
// We trigger a nil-pointer dereference by passing a runner with
// recentNames=nil (Excludes() will NPE on the nil receiver inside
// generateAndPersist).
func TestTickRunner_RecoversFromPanic(t *testing.T) {
	// Build a minimally-shaped runner whose pipeline will panic.
	// We intentionally leave recentNames nil; generateAndPersist calls
	// r.recentNames.Excludes() and panics. The defer-recover in
	// tickRunner must catch this.
	r := &gameTypeRunner{
		// cfg/sel/jackpot left zero — tickRunner shouldn't reach the
		// real generators because the nil-cooldown NPE fires first.
		// (If a future refactor reorders the calls, this test will
		// either still panic earlier or need to be replaced with a
		// different forced-panic source — that's fine.)
	}
	slot := time.Now().UTC()

	// The test passes if tickRunner returns without re-panicking.
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("tickRunner did NOT recover from panic: %v", rec)
		}
	}()

	tickRunner(r, slot, nil, nil)

	// Post-condition: lastSlot was advanced (permanent-class handling),
	// retryCount reset.
	if !r.lastSlot.Equal(slot) {
		t.Errorf("expected lastSlot advanced to %v after panic, got %v",
			slot, r.lastSlot)
	}
	if r.retryCount != 0 {
		t.Errorf("expected retryCount reset to 0 after panic, got %d", r.retryCount)
	}
}

// TestIsTransientErr_Classification covers the table of transient-vs-
// permanent classifications used by runScheduler to decide whether to
// retry the same slot or advance lastSlot.
func TestIsTransientErr_Classification(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		transient bool
	}{
		{"nil", nil, false},
		{"permission denied wrapped", fmt.Errorf("wrap: %w", os.ErrPermission), true},
		{"ENOSPC wrapped", fmt.Errorf("audit append: %w", syscall.ENOSPC), true},
		{"EIO wrapped", fmt.Errorf("write: %w", syscall.EIO), true},
		{"sqlite busy stringly", errors.New("upsert ROUND1: database is locked (5)"), true},
		{"SQLITE_BUSY constant in message", errors.New("SQLITE_BUSY: cannot start a transaction within a transaction"), true},
		{"disk full message", errors.New("disk is full"), true},
		{"no space left on device message", errors.New("write: no space left on device"), true},
		{"shape check permanent", errors.New("generate: generators: len(Odds)=7, want NumberOdds=8"), false},
		{"adapter permanent", errors.New("adapter: invalid finish first=0"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isTransientErr(tc.err)
			if got != tc.transient {
				t.Errorf("isTransientErr(%v) = %v, want %v", tc.err, got, tc.transient)
			}
		})
	}
}


// TestLoadEnv_SeedHexNonHexRejected verifies that a 64-char seed of
// non-hex characters is rejected by loadEnv BEFORE any file is opened.
// Regression for the orphan-audit-file bug pre-Fix 5.
func TestLoadEnv_SeedHexNonHexRejected(t *testing.T) {
	t.Setenv("RACEGEN_SEED_HEX", strings.Repeat("z", 64))
	_, err := loadEnv()
	if err == nil {
		t.Fatalf("expected error for non-hex seed, got nil")
	}
	if !strings.Contains(err.Error(), "RACEGEN_SEED_HEX") {
		t.Errorf("error should mention RACEGEN_SEED_HEX; got: %v", err)
	}
}

// TestLoadEnv_SeedHexValid confirms that a valid 64-hex seed parses.
func TestLoadEnv_SeedHexValid(t *testing.T) {
	valid := hex.EncodeToString(make([]byte, 32))
	t.Setenv("RACEGEN_SEED_HEX", valid)
	e, err := loadEnv()
	if err != nil {
		t.Fatalf("expected nil err for valid hex seed, got %v", err)
	}
	if e.seedHex != valid {
		t.Errorf("seedHex round-trip: got %q want %q", e.seedHex, valid)
	}
}

// TestLoadEnv_SeedHexWrongLength verifies the length check still fires.
func TestLoadEnv_SeedHexWrongLength(t *testing.T) {
	t.Setenv("RACEGEN_SEED_HEX", "deadbeef")
	_, err := loadEnv()
	if err == nil {
		t.Fatalf("expected error for short seed, got nil")
	}
	if !strings.Contains(err.Error(), "64 hex chars") {
		t.Errorf("error should mention length; got: %v", err)
	}
}

// TestLoadEnv_SeedHexEmptyOKInDev verifies that in dev (APP_ENV unset or
// "dev") an empty seed falls through to the crypto/rand path (loadEnv
// accepts it; makeMT handles it).
func TestLoadEnv_SeedHexEmptyOKInDev(t *testing.T) {
	t.Setenv("RACEGEN_SEED_HEX", "")
	t.Setenv("APP_ENV", "dev")
	e, err := loadEnv()
	if err != nil {
		t.Fatalf("expected nil err for empty seed in dev, got %v", err)
	}
	if e.seedHex != "" {
		t.Errorf("expected empty seedHex passthrough, got %q", e.seedHex)
	}
}

// TestLoadEnv_SeedHexEmptyFailClosedInProd verifies the fail-closed rule:
// in prod/staging an empty RACEGEN_SEED_HEX must abort, because a
// non-reproducible run breaks audit-log replay (GLI-19 §3.3).
func TestLoadEnv_SeedHexEmptyFailClosedInProd(t *testing.T) {
	for _, env := range []string{"prod", "production", "staging", "stg"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv("RACEGEN_SEED_HEX", "")
			t.Setenv("APP_ENV", env)
			_, err := loadEnv()
			if err == nil {
				t.Fatalf("expected fail-closed error for empty seed when APP_ENV=%s, got nil", env)
			}
			if !strings.Contains(err.Error(), "RACEGEN_SEED_HEX is required") {
				t.Errorf("error should mention the seed requirement; got: %v", err)
			}
		})
	}
}

// ----------------------------------------------------------------------
// Plan 02 Task 2 — future-horizon scheduler tests
// ----------------------------------------------------------------------
//
// These tests verify the shift from "generate the most recent past slot
// per tick" (legacy nextSlot) to "generate 10 future slots at boot, then
// advance the horizon by one whenever a new intervalSec boundary is
// crossed" (nextFutureSlot + bootBulk + tickHorizon). The downstream
// /tv handler in internal/protocol/protocol.go:56 relies on
// videoStartDt > now to hide `finish` until each round is "live", so a
// regression that emits past-slotted GA rounds would break the
// broadcaster contract documented in
// docs/racegen-design/03-RACE-BROADCASTER.md (Task 2).

// TestNextFutureSlotMonotonic verifies the n-th slot is strictly greater
// than the (n-1)-th for all n>=0 — the property the scheduler relies on
// to drive the horizon forward by exactly one round per crossed tick.
func TestNextFutureSlotMonotonic(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 3, 17, 0, time.UTC) // arbitrary mid-slot moment
	const intervalSec = 240
	prev := nextFutureSlot(now, intervalSec, 0)
	for n := 1; n < 15; n++ {
		got := nextFutureSlot(now, intervalSec, n)
		if !got.After(prev) {
			t.Fatalf("nextFutureSlot(now, %d, %d) = %s NOT after n=%d (%s)",
				intervalSec, n, got, n-1, prev)
		}
		if delta := got.Sub(prev); delta != time.Duration(intervalSec)*time.Second {
			t.Fatalf("n=%d: delta %s, want %ds", n, delta, intervalSec)
		}
		prev = got
	}
}

// TestNextFutureSlotAlignedToInterval verifies every returned slot is a
// multiple of intervalSec measured from unix epoch (i.e. .Unix() % d == 0)
// AND strictly in the future from `now`. The legacy nextSlot returned
// the most recent past boundary; the new contract is the future side.
func TestNextFutureSlotAlignedToInterval(t *testing.T) {
	cases := []struct {
		name        string
		now         time.Time
		intervalSec int
	}{
		{"dog8_midslot", time.Date(2026, 5, 17, 12, 3, 17, 0, time.UTC), 240},
		{"dog6_midslot", time.Date(2026, 5, 17, 12, 3, 17, 0, time.UTC), 240},
		{"on_boundary_240", time.Date(2026, 5, 17, 12, 4, 0, 0, time.UTC), 240},
		{"60s_interval", time.Date(2026, 5, 17, 12, 0, 5, 0, time.UTC), 60},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for n := 0; n < 12; n++ {
				got := nextFutureSlot(c.now, c.intervalSec, n)
				if got.Unix()%int64(c.intervalSec) != 0 {
					t.Errorf("n=%d: got %s (unix=%d) not aligned to %ds",
						n, got, got.Unix(), c.intervalSec)
				}
				if !got.After(c.now) {
					t.Errorf("n=%d: got %s NOT strictly after now=%s",
						n, got, c.now)
				}
			}
		})
	}
}

// TestNextFutureSlotOnBoundary covers the edge case where `now` lands
// exactly on an intervalSec boundary: nextFutureSlot(now, ..., 0) must
// still be strictly in the future (i.e. now + intervalSec), NOT now
// itself. Otherwise the boot bulk's first slot would have
// videoStartDt == now and the protocol filter would skip emitting
// `finish` until the next tick — silently re-introducing the legacy
// "ronda nace ya finalizada" pathology this task fixes.
func TestNextFutureSlotOnBoundary(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 4, 0, 0, time.UTC) // exactly aligned
	got := nextFutureSlot(now, 240, 0)
	want := now.Add(240 * time.Second)
	if !got.Equal(want) {
		t.Fatalf("on-boundary now: got %s, want %s (now+interval)", got, want)
	}
}

// TestScheduledSlotGridAndRollover locks in the load-bearing behavior of
// scheduledSlot documented in main.go: it intentionally passes raceutil a
// race number that may exceed the schedule day's race count (e.g. dog8 361),
// yet the returned slot must always land EXACTLY on raceutil's grid so that
// the broadcaster (which recomputes the code from the slot) can find it.
//
// We sweep `now` across >24h at fine resolution and assert, for every slot
// scheduledSlot produces, the two invariants the GA-betting path depends on:
//  1. the slot is strictly in the future (videoStartDt > now contract); and
//  2. the slot is on raceutil's own grid — feeding the slot's recomputed
//     race number back through VideoStartTime reproduces the slot exactly
//     (this is the self-correction that makes the day/DST boundary work).
//
// It also asserts the pre-rollover branch is actually exercised: at least
// one `now` must yield a slot whose own (next-day) race number is SMALLER
// than the number scheduledSlot fed raceutil — i.e. the number reset across
// the boundary. Without the grid-continuity property this would desync.
func TestScheduledSlotGridAndRollover(t *testing.T) {
	cfg, err := config.Get("dog8")
	if err != nil {
		t.Fatalf("config.Get(dog8): %v", err)
	}
	r := &gameTypeRunner{cfg: cfg}
	gt := cfg.GameType
	interval := time.Duration(cfg.RoundIntervalSec) * time.Second

	// Start a few minutes before the dog8 Malta-epoch UTC instant on 2026-05-17
	// (00:03:30 Malta = 22:03:30 UTC prior day in CEST) and sweep past it, so
	// the window where cur+1 > racesPerDay is covered.
	start := time.Date(2026, 5, 16, 21, 0, 0, 0, time.UTC)
	rolloverSeen := false
	for step := 0; step < 320; step++ { // 320 * 5min = ~26.6h
		now := start.Add(time.Duration(step) * 5 * time.Minute)
		cur := raceutil.CurrentRaceNumber(gt, now)
		if cur <= 0 {
			t.Fatalf("CurrentRaceNumber(%s, %s) = %d, want >=1 (supported gametype)", gt, now, cur)
		}
		prev := now
		for n := 0; n < 6; n++ {
			slot := r.scheduledSlot(now, n)
			if !slot.After(now) {
				t.Fatalf("step=%d n=%d: slot %s NOT strictly after now %s", step, n, slot, now)
			}
			if n > 0 && slot.Sub(prev) != interval {
				t.Fatalf("step=%d n=%d: slot spacing %s, want %s", step, n, slot.Sub(prev), interval)
			}
			prev = slot
			// On-grid self-correction: the slot's OWN number must reproduce it.
			num := raceutil.CurrentRaceNumber(gt, slot)
			if num <= 0 {
				t.Fatalf("step=%d n=%d: slot %s recomputes race number %d", step, n, slot, num)
			}
			if back := raceutil.VideoStartTime(gt, num, slot); !back.Equal(slot) {
				t.Fatalf("step=%d n=%d: slot %s off-grid; VideoStartTime(num=%d) = %s",
					step, n, slot, num, back)
			}
			if raceutil.CurrentRoundCode(gt, slot) == "" {
				t.Fatalf("step=%d n=%d: slot %s yields empty round code", step, n, slot)
			}
			// Pre-rollover detection: scheduledSlot fed raceutil (cur+1+n);
			// if the slot's own number came back smaller, it reset across a
			// schedule-day boundary — the exact case the comment warns about.
			if num < cur+1+n {
				rolloverSeen = true
			}
		}
	}
	if !rolloverSeen {
		t.Fatal("sweep never crossed a schedule-day rollover; test no longer exercises the pre-rollover branch")
	}
}

// TestBootGenerates10FutureRoundsPerGameType runs the boot bulk against
// a temporary SQLite database and verifies that for each supported game
// type there are exactly bulkSize (=10) GA-prefixed rounds, all with
// videoStartDt strictly greater than `now`. This is the boot-time
// invariant race-broadcaster's cold-start relies on.
func TestBootGenerates10FutureRoundsPerGameType(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "racegen-bulk.db")
	auditPath := filepath.Join(tmp, "racegen-bulk.jsonl")

	if err := sqlite.Init(dbPath); err != nil {
		t.Fatalf("sqlite.Init: %v", err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })

	aud, err := audit.Open(auditPath)
	if err != nil {
		t.Fatalf("audit.Open: %v", err)
	}
	t.Cleanup(func() { _ = aud.Close() })

	// Deterministic seed so the test is repeatable.
	mt, err := rng.NewMT19937WithSeedHex(
		"00000000000000000000000000000000000000000000000000000000000000aa")
	if err != nil {
		t.Fatalf("rng.NewMT19937WithSeedHex: %v", err)
	}

	runners, err := buildRunners(config.SupportedGameTypes(), generators.JackpotInitialValue)
	if err != nil {
		t.Fatalf("buildRunners: %v", err)
	}
	if len(runners) < 2 {
		t.Fatalf("expected at least 2 game types, got %d", len(runners))
	}

	now := time.Now().UTC()
	if err := bootBulk(runners, mt, aud, now, bulkSize); err != nil {
		t.Fatalf("bootBulk: %v", err)
	}

	// Verify GA count + videoStartDt > now per game type via raw SQL.
	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, r := range runners {
		var n int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM GameRounds
			 WHERE RoundCode LIKE 'GA%' AND GameType = ?`,
			r.cfg.GameType,
		).Scan(&n); err != nil {
			t.Fatalf("count for %s: %v", r.cfg.GameType, err)
		}
		if n != bulkSize {
			t.Errorf("gameType=%s: got %d GA rounds, want %d", r.cfg.GameType, n, bulkSize)
		}

		// All videoStartDt strictly after now. We use the same
		// "2006-01-02 15:04:05" string layout the adapter writes
		// (see adapter.ToGameRound) so a lexicographic compare in
		// SQLite matches a chronological one.
		bound := now.Format("2006-01-02 15:04:05")
		var stale int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM GameRounds
			 WHERE RoundCode LIKE 'GA%' AND GameType = ?
			   AND VideoStartDt <= ?`,
			r.cfg.GameType, bound,
		).Scan(&stale); err != nil {
			t.Fatalf("stale-check for %s: %v", r.cfg.GameType, err)
		}
		if stale != 0 {
			t.Errorf("gameType=%s: %d GA rounds have VideoStartDt <= now (%s)",
				r.cfg.GameType, stale, bound)
		}
	}
}

// TestTickAdvancesHorizonByOne verifies that after the boot bulk
// pre-generates N rounds, a single scheduler tick which crosses one
// intervalSec boundary produces exactly ONE new round per runner (the
// slot at the far end of the horizon — nextFutureSlot(now, interval,
// bulkSize-1)) rather than re-emitting the whole window.
//
// The companion check — a tick that does NOT cross a boundary leaves
// the count unchanged — is the safeguard against re-generating an
// already-persisted slot on every tick.
func TestTickAdvancesHorizonByOne(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "racegen-tick.db")
	auditPath := filepath.Join(tmp, "racegen-tick.jsonl")

	if err := sqlite.Init(dbPath); err != nil {
		t.Fatalf("sqlite.Init: %v", err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })

	aud, err := audit.Open(auditPath)
	if err != nil {
		t.Fatalf("audit.Open: %v", err)
	}
	t.Cleanup(func() { _ = aud.Close() })

	mt, err := rng.NewMT19937WithSeedHex(
		"00000000000000000000000000000000000000000000000000000000000000bb")
	if err != nil {
		t.Fatalf("rng init: %v", err)
	}

	runners, err := buildRunners(config.SupportedGameTypes(), generators.JackpotInitialValue)
	if err != nil {
		t.Fatalf("buildRunners: %v", err)
	}

	// Pick `now` aligned to a 240s boundary so the first boundary
	// crossing is deterministic; simulate ticks as direct calls to
	// tickHorizon (the per-tick body extracted from runScheduler).
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	if err := bootBulk(runners, mt, aud, now, bulkSize); err != nil {
		t.Fatalf("bootBulk: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	countGA := func(gameType string) int {
		t.Helper()
		var n int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM GameRounds
			 WHERE RoundCode LIKE 'GA%' AND GameType = ?`,
			gameType,
		).Scan(&n); err != nil {
			t.Fatalf("count for %s: %v", gameType, err)
		}
		return n
	}

	baseline := make(map[string]int)
	for _, r := range runners {
		baseline[r.cfg.GameType] = countGA(r.cfg.GameType)
		if baseline[r.cfg.GameType] != bulkSize {
			t.Fatalf("baseline %s: got %d, want %d",
				r.cfg.GameType, baseline[r.cfg.GameType], bulkSize)
		}
	}

	// Pre-boundary tick: now+1s has not crossed an intervalSec
	// boundary, so the horizon slot is unchanged ⇒ no new rounds.
	tickHorizon(runners, mt, aud, now.Add(1*time.Second))
	for _, r := range runners {
		got := countGA(r.cfg.GameType)
		if got != baseline[r.cfg.GameType] {
			t.Errorf("pre-boundary tick: %s went %d → %d (expected unchanged)",
				r.cfg.GameType, baseline[r.cfg.GameType], got)
		}
	}

	// Post-boundary tick: now + (intervalSec + 1)s has crossed exactly
	// one 240s boundary ⇒ exactly ONE new round per runner.
	const interval = 240
	tickHorizon(runners, mt, aud, now.Add(time.Duration(interval+1)*time.Second))
	for _, r := range runners {
		got := countGA(r.cfg.GameType)
		want := baseline[r.cfg.GameType] + 1
		if got != want {
			t.Errorf("post-boundary tick: %s got %d, want %d (+1)",
				r.cfg.GameType, got, want)
		}
	}
}
