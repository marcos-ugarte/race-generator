package generators

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"vg-racegen/internal/racegen/audit"
	"vg-racegen/internal/racegen/config"
	"vg-racegen/internal/racegen/rng"
	"vg-racegen/internal/racegen/videoselector"
	"vg-racegen/internal/raceutil"
)

// Game is the orchestrator's atomic per-round product. It bundles
// identity/scheduling metadata, every Phase 3 sub-block (competitors,
// conditions, odds, finish), and the bonus/jackpot data computed at this
// level. The adapter (Phase 4 Task 13) projects this onto models.GameRound +
// []models.GameResult.
//
// Field ordering is irrelevant for JSON consumers — the adapter selects
// individual fields explicitly. Keep additions stable; downstream tests
// rely on direct field access.
type Game struct {
	// Identity / scheduling.
	RoundCode     string
	GameType      string // "dog8" | "dog6"
	BetofferID    int
	ScheduleID    int
	EventType     string
	RoundInterval int       // seconds
	VideoStartDt  time.Time // UTC
	VideoEndDt    time.Time // UTC
	IDRace        int       // 1-based ordinal of the day

	// Generated content.
	Odds        []float64
	Competitors map[string]Competitor
	Finish      FinishData
	Conditions  Conditions
	Bonus       int // 1 | 2 | 3
	JackpotInfo JackpotInfo
}

// JackpotInfo is the per-round jackpot snapshot embedded in each Game.
// Marshals to the legacy shape expected by Webview clients.
type JackpotInfo struct {
	BonusValue    float64       `json:"bonusValue"`
	OldBonusValue float64       `json:"oldBonusValue"`
	BonusHistory  []JackpotHist `json:"bonusHistory"`
}

// JackpotHist is one entry of the rolling jackpot history. Name is fixed
// to "Virteon Gaming" per legacy game.ts:66; Amount is uniformly drawn
// from [40000, 50000] per legacy formula.
type JackpotHist struct {
	Round  string  `json:"round"`
	ID     string  `json:"id"`
	Date   string  `json:"date"`
	Time   string  `json:"time"`
	Name   string  `json:"name"`
	Amount float64 `json:"amount"`
}

// Jackpot reset constants — mirror legacy
// virteon-platform/apps/ws-server/src/generators/game.ts:38-39. When the
// accumulator reaches JackpotMaxValue, GenerateGame simulates a "win" by
// resetting it to JackpotResetValue + rand*500 (∈ [40000, 40500)). This
// keeps the displayed jackpot bounded and produces visible reset events.
const (
	JackpotInitialValue = 45000.00
	JackpotResetValue   = 40000.00
	JackpotMaxValue     = 55000.00
)

// JackpotState is the orchestrator-owned jackpot accumulator. Callers
// initialize Current at boot (use JackpotInitialValue) and pass &state
// into each GenerateGame call — the call mutates Current. The value is
// non-decreasing between rounds EXCEPT when it crosses JackpotMaxValue,
// at which point it is reset to ∈ [JackpotResetValue, JackpotResetValue+500).
type JackpotState struct {
	Current float64
}

// GenerateGame produces a complete Game for the slot. It orchestrates the
// four Phase 3 sub-generators in the byte-stream-determinism order
// (competitors, conditions, finish, odds — finish precedes odds so the
// odds↔finish coupling can bias favorites toward winning), then draws
// bonus + jackpot, validates internal consistency, and finally emits a
// single audit entry.
//
// IMPORTANT: this function does NOT call rng.StateModifier. GLI-19 §3.2.6
// mandates "background cycling between games"; the caller is responsible
// for invoking the state modifier at the appropriate point in its loop
// (typically between successive GenerateGame calls). Mixing the modifier
// in here would couple per-game determinism to call ordering at the
// caller — undesirable.
//
// Errors are returned (never panicked) so production loops can propagate
// failures upward without crashing the binary. If `aud` is non-nil and
// audit emission fails, the Game is NOT returned — an audit-less Game is
// worthless from a regulatory standpoint. Callers MAY pass a nil `aud`
// for tests and harness code; in production this is a misconfiguration
// that the binary's startup should reject (Task 14's responsibility).
func GenerateGame(
	mt rng.Source,
	cfg config.GameTypeConfigExt,
	sel *videoselector.Selector,
	jp *JackpotState,
	slot time.Time,
	recentNames map[string]bool,
	aud *audit.Log,
) (Game, error) {
	if mt == nil {
		return Game{}, errors.New("generators: nil rng.Source")
	}
	if sel == nil {
		return Game{}, errors.New("generators: nil *videoselector.Selector")
	}
	if jp == nil {
		return Game{}, errors.New("generators: nil *JackpotState")
	}

	slotUTC := slot.UTC()

	// 1. Video start/end window + idRace (computed first because the
	// RoundCode embeds idRace per DS parity).
	if cfg.RoundIntervalSec <= 0 {
		return Game{}, fmt.Errorf("generators: invalid RoundIntervalSec=%d", cfg.RoundIntervalSec)
	}
	// videoEnd is the end of the race VIDEO (videoStart + the real clip
	// length), NOT the end of the 240s slot. DS reports a short video window
	// (dogs 45s, horse 40s) and leaves the rest of RoundIntervalSec as the
	// betting/countdown gap; consumers (web-lobby, TV) use [videoStart,
	// videoEnd) as the LIVE window. Using the full RoundIntervalSec made every
	// GA race read as "live" for the whole 4-minute slot (no betting phase,
	// out of sync with the actual video and with DS). VideoDurationSec restores
	// DS parity. Fallback to RoundIntervalSec only if a config omits it.
	videoDurationSec := cfg.VideoDurationSec
	if videoDurationSec <= 0 {
		videoDurationSec = cfg.RoundIntervalSec
	}
	videoStart := slotUTC
	videoEnd := slotUTC.Add(time.Duration(videoDurationSec) * time.Second)

	// 2. Build the round identity from raceutil — the SAME calibrated source
	// the race-broadcaster (broadcast.go → CurrentRoundCode) and the POS
	// gamepool (raceutil.RaceWindow → GamesByRoundCodesGA) use to look rounds
	// up, and the same numbering the DS vendor emits. Delegating here is what
	// guarantees that (a) the GA round and the DS round for the same
	// wall-clock slot carry the IDENTICAL race number — only the "GA" prefix
	// differs — and (b) the broadcaster's exact-code lookups always hit.
	//
	// Do NOT reintroduce a local secOfDay/UTC-midnight computation: raceutil's
	// epoch is per-game-type Malta-local and DST-aware (dog8 = 00:03:30 Malta),
	// which is ~29 races off from UTC midnight for dog8. The previous
	// secOfDay+1 formula was that bug — it produced codes the broadcaster
	// could never find, so the GA gamepool was empty and GA gameResults never
	// emitted. CurrentRoundCode emits betoffer_schedule_YYYYMMDDNNNN (sample
	// "541_105_202604120260"); we keep the "GA" prefix as the sole
	// discriminator from DS-captured rounds.
	//
	// Invariant: cfg.RoundIntervalSec MUST equal raceutil's interval for this
	// game type (both come from the same prod spec — dog8/dog6 240s), so the
	// generator's slot grid (see scheduledSlot in cmd/race-generator) and
	// raceutil's race grid are the same bijection.
	idRace := raceutil.CurrentRaceNumber(cfg.GameType, slotUTC)
	nonGACode := raceutil.CurrentRoundCode(cfg.GameType, slotUTC)
	if idRace <= 0 || nonGACode == "" {
		return Game{}, fmt.Errorf("generators: raceutil has no schedule for gameType %q", cfg.GameType)
	}
	roundCode := "GA" + nonGACode

	// 4. Phase 3 generators — FIXED ORDER for byte-stream determinism.
	//    Competitors and conditions come first (unchanged). Finish now runs
	//    BEFORE odds because the odds↔finish coupling (config Theta) assigns
	//    the WIN-odds VALUES to physical slots using the chosen finish order
	//    (finish.Order()); favorites must tend to win. With Theta=0 the
	//    coupling is uniform, but the MT consumption order still differs from
	//    the pre-coupling code (finish draws before odds), so oddsHash and
	//    mtSeqAfter in the audit fingerprint below CHANGE vs the legacy
	//    odds-then-finish ordering — this is intended.
	competitors := GenerateCompetitors(mt, cfg, recentNames)
	conditions := GenerateConditions(mt, cfg)
	finish := GenerateFinish(mt, cfg, sel)
	odds := GenerateOdds(mt, cfg, finish.Order())

	// 5. Bonus — single draw, ordered cascade (3x first, then 2x, default 1).
	r := rng.CertifiedFloat(mt)
	var bonus int
	switch {
	case r < cfg.Bonus3xProbability:
		bonus = 3
	case r < cfg.Bonus3xProbability+cfg.Bonus2xProbability:
		bonus = 2
	default:
		bonus = 1
	}

	// 6. Jackpot update — mutates *jp.
	//    - increment ∈ [0, 10) rounded to 2dp.
	//    - When the running total crosses JackpotMaxValue (55000), reset
	//      to JackpotResetValue + rand*500 ∈ [40000, 40500) — legacy
	//      ws-server/src/generators/game.ts:67-69 ("simulated win").
	//    - history entry Amount ∈ [40000, 50000] rounded to 2dp (legacy
	//      game.ts:64-67).
	oldJackpot := jp.Current
	increment := math.Round(rng.CertifiedFloat(mt)*10*100) / 100
	newJackpot := oldJackpot + increment
	if newJackpot >= JackpotMaxValue {
		newJackpot = JackpotResetValue + rng.CertifiedFloat(mt)*500
	}
	jp.Current = math.Round(newJackpot*100) / 100

	histAmount := math.Round((40000+rng.CertifiedFloat(mt)*10000)*100) / 100

	jackpotInfo := JackpotInfo{
		BonusValue:    jp.Current,
		OldBonusValue: oldJackpot,
		BonusHistory: []JackpotHist{
			{
				Round:  fmt.Sprintf("%04d", idRace),
				ID:     hashRoundID(roundCode),
				Date:   slotUTC.Format("2006-01-02"),
				Time:   slotUTC.Format("15:04:05"),
				Name:   "Virteon Gaming",
				Amount: histAmount,
			},
		},
	}

	g := Game{
		RoundCode:     roundCode,
		GameType:      cfg.GameType,
		BetofferID:    cfg.BetofferID,
		ScheduleID:    cfg.ScheduleID,
		EventType:     cfg.EventType,
		RoundInterval: cfg.RoundIntervalSec,
		VideoStartDt:  videoStart,
		VideoEndDt:    videoEnd,
		IDRace:        idRace,
		Odds:          odds,
		Competitors:   competitors,
		Finish:        finish,
		Conditions:    conditions,
		Bonus:         bonus,
		JackpotInfo:   jackpotInfo,
	}

	// 7. Consistency checks (hard errors — never silently emit a malformed
	//    Game).
	if len(g.Odds) != cfg.NumberOdds {
		return Game{}, fmt.Errorf("generators: len(Odds)=%d, want NumberOdds=%d",
			len(g.Odds), cfg.NumberOdds)
	}
	if len(g.Competitors) != cfg.NumberCompetitor {
		return Game{}, fmt.Errorf("generators: len(Competitors)=%d, want NumberCompetitor=%d",
			len(g.Competitors), cfg.NumberCompetitor)
	}
	if len(g.Finish.Finish) != cfg.NumberCompetitor {
		return Game{}, fmt.Errorf("generators: len(Finish.Finish)=%d, want NumberCompetitor=%d",
			len(g.Finish.Finish), cfg.NumberCompetitor)
	}
	if g.Finish.First <= 0 || g.Finish.Second <= 0 {
		return Game{}, fmt.Errorf("generators: invalid finish first=%d second=%d",
			g.Finish.First, g.Finish.Second)
	}

	// 8. Audit emission — exactly one entry per GenerateGame call.
	//    NOTE: oddsHash and mtSeqAfter reflect the post-coupling odds and
	//    the finish-before-odds MT consumption order (see step 4). Both
	//    differ from the legacy odds-then-finish ordering — intended, and
	//    part of the same coupling change.
	if aud != nil {
		payload := map[string]any{
			"roundCode":    g.RoundCode,
			"gameType":     g.GameType,
			"videoStart":   g.VideoStartDt.Format(time.RFC3339Nano),
			"finishFirst":  g.Finish.First,
			"finishSecond": g.Finish.Second,
			"videoID":      ExtractVideoID(g.Finish.VideoName.MP4),
			"bonus":        g.Bonus,
			"oddsHash":     hashShortFloats(g.Odds),
			"compsHash":    hashShortCompetitors(g.Competitors),
			"mtSeqAfter":   mt.GenerationCount(),
		}
		if err := aud.Append(audit.Entry{Kind: "game_generated", Payload: payload}); err != nil {
			return Game{}, fmt.Errorf("generators: audit append: %w", err)
		}
	}

	return g, nil
}

// hashRoundID returns the first 16 hex chars of SHA-256(roundCode).
// Used as the per-history-entry ID. 64 bits of identity is more than
// enough for a rolling history and matches the legacy id-generation
// width.
func hashRoundID(roundCode string) string {
	sum := sha256.Sum256([]byte(roundCode))
	return hex.EncodeToString(sum[:])[:16]
}

// ExtractVideoID parses the trailing R\d+ token from a video URL (e.g.
// "/.local/dog8/R0241_h.mp4" -> "R0241"). If the URL doesn't match the
// expected shape, returns the URL unchanged — the audit log keeps the
// raw value for forensics.
func ExtractVideoID(mp4URL string) string {
	// Find the last '/' and take everything after, up to the first '_' or '.'.
	slash := -1
	for i := len(mp4URL) - 1; i >= 0; i-- {
		if mp4URL[i] == '/' {
			slash = i
			break
		}
	}
	if slash < 0 {
		return mp4URL
	}
	tail := mp4URL[slash+1:]
	end := len(tail)
	for i := 0; i < len(tail); i++ {
		if tail[i] == '_' || tail[i] == '.' {
			end = i
			break
		}
	}
	return tail[:end]
}

// hashShortFloats returns the first 12 hex chars of SHA-256 over the JSON
// encoding of the slice. Order-sensitive — that's the intended audit
// fingerprint.
func hashShortFloats(v []float64) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}

// hashShortCompetitors returns the first 12 hex chars of SHA-256 over a
// canonical, key-sorted JSON encoding of the competitors map. Sorting is
// load-bearing — Go's default map iteration is non-deterministic, so an
// unsorted Marshal would produce a moving fingerprint across runs even
// for byte-identical data.
func hashShortCompetitors(m map[string]Competitor) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// Build a key-ordered slice of {k, v} pairs and marshal that.
	type kv struct {
		K string     `json:"k"`
		V Competitor `json:"v"`
	}
	pairs := make([]kv, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, kv{K: k, V: m[k]})
	}
	raw, err := json.Marshal(pairs)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}
