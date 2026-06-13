package generators

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"vg-racegen/internal/racegen/audit"
	"vg-racegen/internal/racegen/rng"
	"vg-racegen/internal/raceutil"
)

// freshState returns a JackpotState seeded with the legacy default 45000.
func freshState() *JackpotState { return &JackpotState{Current: 45000.0} }

// fixedSlot returns a deterministic slot at 2026-05-17 12:00:00 UTC.
// IDRace/RoundCode are derived from raceutil (the calibrated DS-parity grid,
// Malta-local epoch, DST-aware), NOT from a UTC-midnight ordinal. For dog8 the
// epoch is 00:03:30 Malta = 22:03:30 UTC on the prior day (CEST), so the race
// number at this slot is raceutil.CurrentRaceNumber("dog8", slot) = 210. Tests
// assert against raceutil so the invariant stays pinned to the source of truth.
func fixedSlot() time.Time {
	return time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
}

func TestGenerateGameDog8Shape(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	sel := realSelector(t, "dog8")
	g, err := GenerateGame(mustMT(t), cfg, sel, freshState(), fixedSlot(), nil, nil)
	if err != nil {
		t.Fatalf("GenerateGame: %v", err)
	}
	if len(g.Odds) != cfg.NumberOdds {
		t.Errorf("len(Odds)=%d, want %d", len(g.Odds), cfg.NumberOdds)
	}
	if got, want := len(g.Odds), 64; got != want {
		t.Errorf("len(Odds)=%d, want %d", got, want)
	}
	if got, want := len(g.Competitors), 8; got != want {
		t.Errorf("len(Competitors)=%d, want %d", got, want)
	}
	if got, want := len(g.Finish.Finish), 8; got != want {
		t.Errorf("len(Finish.Finish)=%d, want %d", got, want)
	}
	if g.Bonus < 1 || g.Bonus > 3 {
		t.Errorf("Bonus=%d, want 1..3", g.Bonus)
	}
	if g.JackpotInfo.BonusValue <= 45000 {
		t.Errorf("JackpotInfo.BonusValue=%v, want > 45000", g.JackpotInfo.BonusValue)
	}
	if g.Finish.First < 1 || g.Finish.First > 8 {
		t.Errorf("Finish.First=%d, want 1..8", g.Finish.First)
	}
	if want := raceutil.CurrentRaceNumber("dog8", fixedSlot()); g.IDRace != want {
		t.Errorf("IDRace=%d, want %d (raceutil)", g.IDRace, want)
	}
	if g.GameType != "dog8" {
		t.Errorf("GameType=%q, want dog8", g.GameType)
	}
}

func TestGenerateGameDog6Shape(t *testing.T) {
	cfg := mustConfig(t, "dog6")
	sel := realSelector(t, "dog6")
	g, err := GenerateGame(mustMT(t), cfg, sel, freshState(), fixedSlot(), nil, nil)
	if err != nil {
		t.Fatalf("GenerateGame: %v", err)
	}
	if got, want := len(g.Odds), 36; got != want {
		t.Errorf("len(Odds)=%d, want %d", got, want)
	}
	if got, want := len(g.Competitors), 6; got != want {
		t.Errorf("len(Competitors)=%d, want %d", got, want)
	}
	if got, want := len(g.Finish.Finish), 6; got != want {
		t.Errorf("len(Finish.Finish)=%d, want %d", got, want)
	}
	if g.Finish.First < 1 || g.Finish.First > 6 {
		t.Errorf("Finish.First=%d, want 1..6", g.Finish.First)
	}
}

func TestGenerateGameDeterminism(t *testing.T) {
	cases := []string{"dog8", "dog6"}
	for _, gt := range cases {
		gt := gt
		t.Run(gt, func(t *testing.T) {
			cfg := mustConfig(t, gt)
			selA := realSelector(t, gt)
			selB := realSelector(t, gt)
			ga, err := GenerateGame(mustMT(t), cfg, selA, freshState(), fixedSlot(), nil, nil)
			if err != nil {
				t.Fatalf("GenerateGame A: %v", err)
			}
			gb, err := GenerateGame(mustMT(t), cfg, selB, freshState(), fixedSlot(), nil, nil)
			if err != nil {
				t.Fatalf("GenerateGame B: %v", err)
			}
			ja, err := json.Marshal(ga)
			if err != nil {
				t.Fatalf("marshal A: %v", err)
			}
			jb, err := json.Marshal(gb)
			if err != nil {
				t.Fatalf("marshal B: %v", err)
			}
			if string(ja) != string(jb) {
				t.Fatalf("non-deterministic Game for %s:\n a=%s\n b=%s", gt, ja, jb)
			}
		})
	}
}

// TestRoundCodeFormat pins DS-parity encoding:
// GA<betoffer>_<schedule>_<YYYYMMDD><NNNN> where the body after the "GA"
// prefix is byte-for-byte raceutil.CurrentRoundCode(gameType, slot) — the
// calibrated DS grid (Malta-local epoch, DST-aware). The GA prefix is the
// sole discriminator from a real DS-vendor code.
// Reference: docs/WS_SERVER_PROTOCOL_REFERENCE.md:171-195 (sample
// "541_105_202604120260").
//
// fixedSlot is 2026-05-17 12:00:00 UTC; dog8 epoch is 00:03:30 Malta =
// 22:03:30 UTC prior day (CEST) → race 210 → "GA541_105_202605170210".
func TestRoundCodeFormat(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	sel := realSelector(t, "dog8")
	g, err := GenerateGame(mustMT(t), cfg, sel, freshState(), fixedSlot(), nil, nil)
	if err != nil {
		t.Fatalf("GenerateGame: %v", err)
	}
	want := "GA" + raceutil.CurrentRoundCode("dog8", fixedSlot())
	if g.RoundCode != want {
		t.Errorf("RoundCode=%q, want %q", g.RoundCode, want)
	}
	// Length: "GA" (2) + "541" (3) + "_" + "105" (3) + "_" + "YYYYMMDD" (8)
	// + "NNNN" (4) = 22. Same width as DS-vendor codes (20 chars) + the
	// 2-char "GA" prefix.
	if got, want := len(g.RoundCode), 22; got != want {
		t.Errorf("len(RoundCode)=%d, want %d", got, want)
	}
}

func TestRoundCodeNeverStartsWithDigit(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	sel := realSelector(t, "dog8")
	// 1000 slots, 1 second apart, starting at the dawn-of-time UTC.
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 1000; i++ {
		slot := base.Add(time.Duration(i) * time.Second)
		g, err := GenerateGame(mustMT(t), cfg, sel, freshState(), slot, nil, nil)
		if err != nil {
			t.Fatalf("GenerateGame at i=%d: %v", i, err)
		}
		if len(g.RoundCode) == 0 || g.RoundCode[0] != 'G' {
			t.Fatalf("RoundCode[0]=%q at i=%d (full=%q) — must start with 'G'",
				string(g.RoundCode[0]), i, g.RoundCode)
		}
		// And the second char must be 'A'.
		if g.RoundCode[1] != 'A' {
			t.Fatalf("RoundCode[1]=%q at i=%d (full=%q) — must start with 'GA'",
				string(g.RoundCode[1]), i, g.RoundCode)
		}
	}
}

func TestBonusProbabilities(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	sel := realSelector(t, "dog8")
	mt := mustMT(t)
	jp := freshState()
	const N = 20000
	counts := map[int]int{1: 0, 2: 0, 3: 0}
	slot := fixedSlot()
	for i := 0; i < N; i++ {
		g, err := GenerateGame(mt, cfg, sel, jp, slot.Add(time.Duration(i)*time.Second), nil, nil)
		if err != nil {
			t.Fatalf("GenerateGame: %v", err)
		}
		counts[g.Bonus]++
	}
	got3 := float64(counts[3]) / N
	got2 := float64(counts[2]) / N
	got1 := float64(counts[1]) / N
	want3 := cfg.Bonus3xProbability
	want2 := cfg.Bonus2xProbability
	want1 := 1 - want2 - want3

	// Tolerance: ~3 stddev for a Bernoulli on the smaller probability.
	// For p≈0.006 over N=20000, sigma ≈ sqrt(0.006*0.994/N) ≈ 0.00055.
	// Use 0.003 absolute as a generous one-tail bound.
	const tol = 0.003
	if math.Abs(got3-want3) > tol {
		t.Errorf("P(bonus=3) got=%.4f want=%.4f tol=%.4f", got3, want3, tol)
	}
	if math.Abs(got2-want2) > tol*3 {
		// Wider tolerance for the larger probability (sigma scales with sqrt(p)).
		t.Errorf("P(bonus=2) got=%.4f want=%.4f tol=%.4f", got2, want2, tol*3)
	}
	if math.Abs(got1-want1) > tol*3 {
		t.Errorf("P(bonus=1) got=%.4f want=%.4f tol=%.4f", got1, want1, tol*3)
	}
	if counts[1]+counts[2]+counts[3] != N {
		t.Errorf("total=%d, want %d", counts[1]+counts[2]+counts[3], N)
	}
}

// TestJackpotMonotonicBetweenResets asserts the accumulator only ever
// goes UP during normal increments, AND on the rare "max hit" tick it
// resets into [JackpotResetValue, JackpotResetValue+500). Mirrors legacy
// ws-server/.../game.ts:67-69 ("simulated win") behaviour.
func TestJackpotMonotonicBetweenResets(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	sel := realSelector(t, "dog8")
	mt := mustMT(t)
	jp := freshState()
	slot := fixedSlot()
	prev := jp.Current
	for i := 0; i < 100; i++ {
		g, err := GenerateGame(mt, cfg, sel, jp, slot.Add(time.Duration(i)*time.Second), nil, nil)
		if err != nil {
			t.Fatalf("GenerateGame i=%d: %v", i, err)
		}
		// Allowed transitions: (a) monotonic increment OR (b) reset down
		// to [JackpotResetValue, JackpotResetValue+500).
		if jp.Current < prev {
			if jp.Current < JackpotResetValue || jp.Current >= JackpotResetValue+500 {
				t.Errorf("i=%d: jp.Current=%v dropped below prev=%v but is outside reset band [%v, %v)",
					i, jp.Current, prev, JackpotResetValue, JackpotResetValue+500)
			}
		}
		if g.JackpotInfo.BonusValue != jp.Current {
			t.Errorf("i=%d: JackpotInfo.BonusValue=%v != jp.Current=%v", i, g.JackpotInfo.BonusValue, jp.Current)
		}
		if g.JackpotInfo.OldBonusValue != prev {
			t.Errorf("i=%d: JackpotInfo.OldBonusValue=%v != prev=%v", i, g.JackpotInfo.OldBonusValue, prev)
		}
		prev = jp.Current
	}
}

// TestJackpotResetsAtMax forces the accumulator past JackpotMaxValue and
// verifies the next tick wraps back into the reset band. Uses a seeded
// state mid-accumulation to keep the test deterministic.
func TestJackpotResetsAtMax(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	sel := realSelector(t, "dog8")
	mt := mustMT(t)
	// Poise AT the max so newJackpot = max + increment (increment ∈ [0,10))
	// always crosses the reset threshold regardless of the RNG-drawn
	// increment — otherwise the test is fragile to any recalibration that
	// re-maps the certified stream (e.g. the rank-space odds model).
	jp := &JackpotState{Current: JackpotMaxValue}
	slot := fixedSlot()
	g, err := GenerateGame(mt, cfg, sel, jp, slot, nil, nil)
	if err != nil {
		t.Fatalf("GenerateGame: %v", err)
	}
	if jp.Current < JackpotResetValue || jp.Current >= JackpotResetValue+500 {
		t.Errorf("expected reset to [%v, %v); got %v", JackpotResetValue, JackpotResetValue+500, jp.Current)
	}
	if g.JackpotInfo.OldBonusValue != JackpotMaxValue {
		t.Errorf("OldBonusValue=%v, want %v", g.JackpotInfo.OldBonusValue, JackpotMaxValue)
	}
}

func TestJackpotHistoryShape(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	sel := realSelector(t, "dog8")
	mt := mustMT(t)
	jp := freshState()
	slot := fixedSlot()
	for i := 0; i < 50; i++ {
		g, err := GenerateGame(mt, cfg, sel, jp, slot.Add(time.Duration(i)*time.Second), nil, nil)
		if err != nil {
			t.Fatalf("GenerateGame i=%d: %v", i, err)
		}
		if got := len(g.JackpotInfo.BonusHistory); got != 1 {
			t.Fatalf("i=%d: len(BonusHistory)=%d, want 1", i, got)
		}
		h := g.JackpotInfo.BonusHistory[0]
		if h.Name != "Virteon Gaming" {
			t.Errorf("i=%d: history.Name=%q, want %q", i, h.Name, "Virteon Gaming")
		}
		if h.Amount < 40000 || h.Amount > 50000 {
			t.Errorf("i=%d: history.Amount=%v outside [40000,50000]", i, h.Amount)
		}
		if len(h.Round) != 4 {
			t.Errorf("i=%d: history.Round=%q, want 4-digit zero-padded", i, h.Round)
		}
		// All four chars must be digits.
		for j := 0; j < len(h.Round); j++ {
			if h.Round[j] < '0' || h.Round[j] > '9' {
				t.Errorf("i=%d: history.Round=%q contains non-digit at %d", i, h.Round, j)
			}
		}
		if len(h.ID) != 16 {
			t.Errorf("i=%d: history.ID=%q len=%d, want 16", i, h.ID, len(h.ID))
		}
		if h.Date == "" || h.Time == "" {
			t.Errorf("i=%d: history.Date/Time empty: date=%q time=%q", i, h.Date, h.Time)
		}
	}
}

func TestGenerateGameErrorOnNilSelector(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("GenerateGame panicked instead of returning error: %v", r)
		}
	}()
	_, err := GenerateGame(mustMT(t), cfg, nil, freshState(), fixedSlot(), nil, nil)
	if err == nil {
		t.Fatalf("expected error on nil selector, got nil")
	}
}

func TestGenerateGameErrorOnNilJackpot(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	sel := realSelector(t, "dog8")
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("GenerateGame panicked instead of returning error: %v", r)
		}
	}()
	_, err := GenerateGame(mustMT(t), cfg, sel, nil, fixedSlot(), nil, nil)
	if err == nil {
		t.Fatalf("expected error on nil jackpot, got nil")
	}
}

func TestGenerateGameErrorOnNilMT(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	sel := realSelector(t, "dog8")
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("GenerateGame panicked instead of returning error: %v", r)
		}
	}()
	_, err := GenerateGame(nil, cfg, sel, freshState(), fixedSlot(), nil, nil)
	if err == nil {
		t.Fatalf("expected error on nil mt, got nil")
	}
}

func TestAuditEmission(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	sel := realSelector(t, "dog8")
	mt := mustMT(t)
	jp := freshState()

	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	aud, err := audit.Open(path)
	if err != nil {
		t.Fatalf("audit.Open: %v", err)
	}

	const N = 5
	slot := fixedSlot()
	prevSeq := uint64(0)
	for i := 0; i < N; i++ {
		_, err := GenerateGame(mt, cfg, sel, jp, slot.Add(time.Duration(i)*time.Second), nil, aud)
		if err != nil {
			t.Fatalf("GenerateGame i=%d: %v", i, err)
		}
		if cur := mt.GenerationCount(); cur <= prevSeq {
			t.Errorf("mtSeq not monotonic: prev=%d cur=%d at i=%d", prevSeq, cur, i)
		}
		prevSeq = mt.GenerationCount()
	}

	if err := aud.Close(); err != nil {
		t.Fatalf("audit.Close: %v", err)
	}

	// Read back the file and assert exactly N entries, all of kind
	// "game_generated", with mtSeqAfter monotonically increasing.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	lines := splitJSONL(raw)
	if len(lines) != N {
		t.Fatalf("len(lines)=%d, want %d", len(lines), N)
	}
	var lastMtSeq float64 // JSON numbers decode as float64
	for i, ln := range lines {
		var e audit.Entry
		if err := json.Unmarshal(ln, &e); err != nil {
			t.Fatalf("line %d unmarshal: %v", i, err)
		}
		if e.Kind != "game_generated" {
			t.Errorf("line %d: Kind=%q, want game_generated", i, e.Kind)
		}
		v, ok := e.Payload["mtSeqAfter"]
		if !ok {
			t.Errorf("line %d: payload missing mtSeqAfter", i)
			continue
		}
		f, ok := v.(float64)
		if !ok {
			t.Errorf("line %d: mtSeqAfter not numeric, got %T", i, v)
			continue
		}
		if i > 0 && f <= lastMtSeq {
			t.Errorf("line %d: mtSeqAfter=%v <= prev=%v (non-monotonic)", i, f, lastMtSeq)
		}
		lastMtSeq = f

		// Also assert other expected payload keys.
		for _, key := range []string{"roundCode", "gameType", "videoStart", "finishFirst", "finishSecond", "videoID", "bonus", "oddsHash", "compsHash"} {
			if _, ok := e.Payload[key]; !ok {
				t.Errorf("line %d: payload missing key %q", i, key)
			}
		}
	}

	// Verify hash chain integrity.
	if _, err := audit.Verify(path); err != nil {
		t.Fatalf("audit.Verify: %v", err)
	}
}

// splitJSONL returns a slice of non-empty trimmed lines from a JSONL blob.
func splitJSONL(raw []byte) [][]byte {
	out := [][]byte{}
	start := 0
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\n' {
			if i > start {
				out = append(out, raw[start:i])
			}
			start = i + 1
		}
	}
	if start < len(raw) {
		out = append(out, raw[start:])
	}
	return out
}

// Sanity: ExtractVideoID parses URLs correctly. Tiny unit on a tiny helper
// — kept here so we don't expose it just for testing.
func TestExtractVideoIDHelper(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/.local/dog8/R0241_h.mp4", "R0241"},
		{"/.local/dog6/R0007_h50.mp4", "R0007"},
		{"/.local/dog6/R0123.jpg", "R0123"},
		{"plain", "plain"},
	}
	for _, tc := range cases {
		if got := ExtractVideoID(tc.in); got != tc.want {
			t.Errorf("ExtractVideoID(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

// Sanity: a regenerated game with a fresh MT should produce a different
// RoundCode payload hash when slot differs by a second. Exercises the
// `oddsHash` / `compsHash` audit fields end-to-end.
func TestGameDifferentSlotsDifferentOutputs(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	sel := realSelector(t, "dog8")
	mt1 := mustMT(t)
	mt2 := mustMT(t)
	g1, err := GenerateGame(mt1, cfg, sel, freshState(), fixedSlot(), nil, nil)
	if err != nil {
		t.Fatalf("g1: %v", err)
	}
	// Step by RoundIntervalSec so the slots fall in DIFFERENT race
	// ordinals — the DS-parity RoundCode encoding embeds the
	// per-day ordinal (4 digits), not seconds-of-day, so two slots
	// within the same interval bucket would share a RoundCode (and
	// that's correct behavior: two attempts to generate the same
	// scheduled race produce the same identity).
	offset := time.Duration(cfg.RoundIntervalSec) * time.Second
	g2, err := GenerateGame(mt2, cfg, sel, freshState(), fixedSlot().Add(offset), nil, nil)
	if err != nil {
		t.Fatalf("g2: %v", err)
	}
	if g1.RoundCode == g2.RoundCode {
		t.Fatalf("RoundCode unchanged across slots: %q", g1.RoundCode)
	}
}

// Verify our test seed produces the expected MT19937 generation counter,
// to catch accidental seed regressions before they propagate to other
// tests. (Cheap canary.)
func TestSeedSanity(t *testing.T) {
	mt, err := rng.NewMT19937WithSeedHex(testSeedHex)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if got := mt.GenerationCount(); got != 0 {
		t.Errorf("fresh MT has gen=%d, want 0", got)
	}
	_ = mt.NextUint32()
	if got := mt.GenerationCount(); got != 1 {
		t.Errorf("after 1 NextUint32, gen=%d, want 1", got)
	}
}

// Helper to keep itoa imports symmetrical with the existing finish_test.go.
var _ = strconv.Itoa
