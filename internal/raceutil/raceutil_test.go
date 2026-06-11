package raceutil

import (
	"testing"
	"time"
)

// mustParse is a compact helper for building RFC3339 moments.
func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tm
}

func TestCurrentRaceNumber_Dog6(t *testing.T) {
	// dog6: Malta start 00:00, interval 240s.
	// CET (winter) → Malta = UTC+1, epoch = yesterday 23:00 UTC.
	// CEST (summer) → Malta = UTC+2, epoch = yesterday 22:00 UTC.

	winter := mustParse(t, "2026-02-15T00:00:00Z") // Feb → CET
	// At 00:00 UTC in winter, Malta is 01:00, so elapsed = 3600s since epoch.
	// Race = floor(3600/240) + 1 = 15 + 1 = 16.
	if got := CurrentRaceNumber("dog6", winter); got != 16 {
		t.Errorf("winter midnight UTC: got race %d, want 16", got)
	}

	summer := mustParse(t, "2026-07-15T00:00:00Z") // July → CEST
	// At 00:00 UTC in summer, Malta is 02:00 (UTC+2), so elapsed = 7200s.
	// Race = floor(7200/240) + 1 = 30 + 1 = 31.
	if got := CurrentRaceNumber("dog6", summer); got != 31 {
		t.Errorf("summer midnight UTC: got race %d, want 31", got)
	}
}

func TestCurrentRaceNumber_Horse(t *testing.T) {
	// horse: Malta start 00:00, interval 320s.
	now := mustParse(t, "2026-07-15T02:00:00Z") // 2h after Malta midnight in CEST
	// Malta is 04:00, epoch = 02:00 Malta UTC-2 = 00:00 UTC? Wait:
	// epoch = yesterday 22:00 UTC (because Malta 00:00 → UTC 22:00 in CEST).
	// Elapsed from yesterday 22:00 to today 02:00 = 4h = 14400s.
	// Race = floor(14400/320) + 1 = 45 + 1 = 46.
	if got := CurrentRaceNumber("horse", now); got != 46 {
		t.Errorf("horse CEST 02:00 UTC: got race %d, want 46", got)
	}
}

func TestCurrentRaceNumber_UnknownGame(t *testing.T) {
	now := mustParse(t, "2026-04-15T12:00:00Z")
	if got := CurrentRaceNumber("unknown", now); got != 0 {
		t.Errorf("unknown game should return 0, got %d", got)
	}
}

func TestCETCESTTransition(t *testing.T) {
	// CEST begins last Sunday of March at 02:00 CET (→ 03:00 CEST), so on
	// 2026-03-29 at 01:00 UTC the clock jumps from 02:00 Malta → 03:00 Malta.
	// This is the case where a naive "cached offset" would desync.
	beforeDST := mustParse(t, "2026-03-29T00:30:00Z") // before spring-forward
	afterDST := mustParse(t, "2026-03-29T02:30:00Z")  // after spring-forward (02:30 UTC = 04:30 CEST)

	r1 := CurrentRaceNumber("dog6", beforeDST)
	r2 := CurrentRaceNumber("dog6", afterDST)
	if r1 == 0 || r2 == 0 {
		t.Fatalf("unexpected zero race number: r1=%d r2=%d", r1, r2)
	}
	if r2 <= r1 {
		t.Errorf("race number should monotonically advance across DST: r1=%d r2=%d", r1, r2)
	}
}

func TestCurrentRoundCode_Format(t *testing.T) {
	now := mustParse(t, "2026-04-15T12:00:00Z")
	code := CurrentRoundCode("dog6", now)
	// Format: {betofferId}_{scheduleId}_{YYYYMMDD}{raceNum:0000}
	// dog6 → betoffer 141, scheduleId 101, date = 2026-04-15.
	const wantPrefix = "141_101_"
	if len(code) < len(wantPrefix)+len("202604150000") {
		t.Fatalf("code %q too short", code)
	}
	if code[:len(wantPrefix)] != wantPrefix {
		t.Errorf("expected prefix %q, got %q", wantPrefix, code[:len(wantPrefix)])
	}
	datePart := code[len(wantPrefix) : len(wantPrefix)+8]
	if datePart != "20260415" {
		t.Errorf("expected date 20260415, got %q", datePart)
	}
}

func TestCurrentRoundCode_UnknownGame(t *testing.T) {
	now := mustParse(t, "2026-04-15T12:00:00Z")
	if got := CurrentRoundCode("unknown", now); got != "" {
		t.Errorf("unknown game should return empty code, got %q", got)
	}
}

func TestVideoStartTime_Alignment(t *testing.T) {
	// Video start time for race N must equal epoch + (N-1)*interval.
	// Race 1 at Malta 00:00 in CEST → UTC 22:00 the previous day.
	now := mustParse(t, "2026-07-15T12:00:00Z") // CEST
	vst := VideoStartTime("dog6", 1, now)
	// Epoch for dog6 on 2026-07-15 CEST = 2026-07-14T22:00:00Z.
	want := mustParse(t, "2026-07-14T22:00:00Z")
	if !vst.Equal(want) {
		t.Errorf("race 1 video start: got %s, want %s", vst, want)
	}

	// Race 100 = epoch + 99*240s.
	vst100 := VideoStartTime("dog6", 100, now)
	want100 := want.Add(99 * 240 * time.Second)
	if !vst100.Equal(want100) {
		t.Errorf("race 100 video start: got %s, want %s", vst100, want100)
	}
}

func TestVideoStartTime_Horse(t *testing.T) {
	// Horse interval = 320s. Race 2 = epoch + 320s.
	now := mustParse(t, "2026-02-15T12:00:00Z") // CET
	vst := VideoStartTime("horse", 2, now)
	// CET → Malta=UTC+1, epoch = yesterday 23:00 UTC.
	epoch := mustParse(t, "2026-02-14T23:00:00Z")
	want := epoch.Add(320 * time.Second)
	if !vst.Equal(want) {
		t.Errorf("horse race 2: got %s, want %s", vst, want)
	}
}

func TestRaceWindow_CurrentSurrounded(t *testing.T) {
	now := mustParse(t, "2026-04-15T12:00:00Z")
	codes := RaceWindow("dog6", now, 2, 3)
	currentNum := CurrentRaceNumber("dog6", now)
	// Window size: history + 1 + future = 2 + 1 + 3 = 6 (assuming currentNum > 2).
	if currentNum <= 2 {
		t.Skipf("currentNum %d too early in day to test full history window", currentNum)
	}
	if len(codes) != 6 {
		t.Errorf("expected 6 codes (-2/+3 around current), got %d", len(codes))
	}
}

func TestRaceWindow_TruncatesHistoryAtOne(t *testing.T) {
	// At epoch start the window can't go below race 1.
	// Spring CEST midnight UTC → Malta 02:00 → race ~31; use epoch directly.
	// Pick a moment very close to epoch.
	// Epoch in CET = yesterday 23:00 UTC.
	now := mustParse(t, "2026-02-14T23:00:30Z") // 30s after epoch in CET
	codes := RaceWindow("dog6", now, 5, 2)
	// current ~= 1. history=5 should truncate to 0, so window = 1..1+2 = 3.
	if len(codes) < 1 {
		t.Fatalf("expected at least 1 code, got %d", len(codes))
	}
	// First code must encode race 0001.
	c := codes[0]
	tail := c[len(c)-4:]
	if tail != "0001" {
		t.Errorf("first window code should end in 0001, got %q (codes=%v)", tail, codes)
	}
}

func TestRaceWindow_UnknownGame(t *testing.T) {
	now := mustParse(t, "2026-04-15T12:00:00Z")
	if got := RaceWindow("unknown", now, 2, 2); got != nil {
		t.Errorf("unknown game should return nil, got %v", got)
	}
}

func TestCurrentRaceNumber_BeforeEpoch(t *testing.T) {
	// Inside scheduleStartUTCAt, if "now" is before today's start, we fall
	// back to yesterday's. So race 1 should be returned consistently near the
	// transition — not a negative number.
	// Pick a moment 1ms after the epoch.
	near := mustParse(t, "2026-02-14T23:00:00Z").Add(time.Millisecond)
	if got := CurrentRaceNumber("dog6", near); got != 1 {
		t.Errorf("just after epoch: got %d, want 1", got)
	}
}
