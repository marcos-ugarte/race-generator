// Command race-generator: scheduler standalone que produce rondas
// procedurales con prefijo "GA" y las escribe en relay.db.
//
// Coexiste con la captura DS sin colisiones de RoundCode: este binario
// escribe solo "GA541_105_*" y "GA141_101_*" (dog8 + dog6 v1), mientras
// que collector, collector-classic y otros escriben "141_*", "241_*",
// "251_*", "541_*", "741_*". Prefijos disjuntos sobre RoundCode (PK)
// garantizan multi-writer safety bajo WAL (verificado vs.
// cmd/collector-classic/main.go:15-18, mismo modelo de aislamiento).
//
// Variables de entorno: ver bloque loadEnv() abajo.
//   - DB_PATH                ruta a relay.db (default ./data/relay.db)
//   - RACEGEN_GAMETYPES      coma-separados (default = config.SupportedGameTypes())
//   - RACEGEN_SEED_HEX       64 hex. SOLO builds -tags gli_lab (replay del
//     laboratorio); un build de produccion ABORTA si esta presente.
//   - APP_ENV                prod | staging | stg | dev (informativo)
//   - RACEGEN_AUDIT_PATH     default ./data/racegen-audit.jsonl
//   - RACEGEN_JACKPOT_INIT   default = generators.JackpotInitialValue
//   - RACEGEN_TICK_MS        frecuencia del scheduler (default 1000)
package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vg-racegen/internal/racegen/adapter"
	"vg-racegen/internal/racegen/audit"
	"vg-racegen/internal/racegen/config"
	"vg-racegen/internal/racegen/data"
	"vg-racegen/internal/racegen/generators"
	"vg-racegen/internal/racegen/rng"
	"vg-racegen/internal/racegen/videoselector"
	"vg-racegen/internal/raceutil"
	"vg-racegen/internal/sqlite"
)

// envConfig agrupa las variables de entorno leidas al arrancar.
type envConfig struct {
	dbPath      string
	gameTypes   []string
	seedHex     string // vacio => sembrar con crypto/rand
	auditPath   string
	jackpotInit float64
	tickMS      int
	horizon     int // RACEGEN_HORIZON: futuras pre-generadas por gameType
}

// bulkSize is the number of future rounds pre-generated PER game type at
// boot, and equivalently the horizon depth maintained per tick. It is a
// resilience buffer: the TV webview only needs 2 future rounds to render
// (see internal/raceserver/handle_tv.go), but a deep horizon means the
// /tv pool never starves even if the generator restarts or hiccups for
// several minutes — there are already `bulkSize` rounds sitting in the DB.
// Default 25 ≈ 100 min (25×240s) of buffer. Overridable via RACEGEN_HORIZON
// (set from env in main() before bootBackfill/bootBulk).
var bulkSize = 25

// version and commit are injected at build time via
//
//	-ldflags "-X main.version=... -X main.commit=..."
//
// (see Dockerfile / Makefile). Defaults keep `go run` working.
var (
	version = "dev"
	commit  = "unknown"
)

// bootBackfillPast is how many already-finished past rounds (plus the
// current in-progress one) bootBackfill generates before the future
// horizon. The /tv window GamesWindowGA(1,3) needs the in-progress round
// + at least one finished round to exist, or the webview has no finished
// game to render (cold-start crash) / sees a gap (Race Break). Generating
// cur-N..cur on boot guarantees a CONTIGUOUS timeline from the recent past
// through the horizon, idempotently (already-persisted slots are upsert-skipped).
const bootBackfillPast = 2

// gameTypeRunner es la unidad de scheduling por gameType. Cada runner
// mantiene su propio Selector (IPF pre-fitted), jackpot, cooldown de
// nombres y "slot mas reciente generado" — pero todos los runners
// comparten el mismo *rng.MT19937 (rng.ModifyStateBetweenGames garantiza
// independencia entre rondas via background cycling).
//
// All runners execute serially in runScheduler's single goroutine —
// no mutex needed on runner fields (recentNames, lastSlot, jackpot,
// retryCount).
//
// `lastSlot` semantics (Plan 02 Task 2): post-refactor this field holds
// the most recent slot persisted by THIS runner — i.e. the far edge of
// the future horizon (boot bulk's last slot, then incremented by one
// each time a tick crosses an intervalSec boundary). The scheduler uses
// it to gate re-generation: a tick whose computed horizon slot is not
// strictly after lastSlot is a no-op.
type gameTypeRunner struct {
	cfg         config.GameTypeConfigExt
	sel         *videoselector.Selector
	jackpot     *generators.JackpotState
	recentNames *nameCooldown
	lastSlot    time.Time
	// retryCount counts consecutive transient failures on the same slot.
	// Reset to 0 on success. After 3 retries we advance lastSlot anyway
	// to avoid an infinite hot-loop on a permanently-failing slot.
	retryCount int
}

// nameCooldown moved to internal/racegen/generators (NameCooldown) so the
// GLI extraction harness replicates the exact production draw sequence.
// Thin aliases keep this binary's call sites and tests unchanged.
type nameCooldown = generators.NameCooldown

func newNameCooldown(capacity int) *nameCooldown {
	return generators.NewNameCooldown(capacity)
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.LUTC)
	log.Printf("race-generator: version=%s commit=%s", version, commit)

	env, err := loadEnv()
	if err != nil {
		log.Fatalf("race-generator: env: %v", err)
	}

	// Audit log + parent dir.
	if err := os.MkdirAll(filepath.Dir(env.auditPath), 0o750); err != nil {
		log.Fatalf("race-generator: mkdir audit dir: %v", err)
	}
	aud, err := audit.Open(env.auditPath)
	if err != nil {
		log.Fatalf("race-generator: audit open: %v", err)
	}
	defer func() { _ = aud.Close() }()

	// SQLite (relay.db). Tambien crea el directorio padre si hace falta.
	if err := os.MkdirAll(filepath.Dir(env.dbPath), 0o750); err != nil {
		log.Fatalf("race-generator: mkdir db dir: %v", err)
	}
	if err := sqlite.Init(env.dbPath); err != nil {
		log.Fatalf("race-generator: sqlite init %s: %v", env.dbPath, err)
	}
	defer func() { _ = sqlite.Close() }()

	// RNG — UNA sola fuente certificada compartida entre runners
	// (HMAC-DRBG SHA-256 en producción; ver source_prod.go / source_lab.go).
	mt, srcDesc, err := makeSource(env.seedHex)
	if err != nil {
		log.Fatalf("race-generator: rng init: %v", err)
	}

	// Audit `init` entry. NUNCA registra material de semilla (GLI-19 R4/R5
	// — hallazgo H3): el descriptor identifica la fuente y, en builds de
	// laboratorio, un fingerprint SHA-256 de la semilla.
	if err := aud.Append(audit.Entry{
		Kind: "init",
		Payload: map[string]any{
			"rngSource":   srcDesc,
			"dbPath":      env.dbPath,
			"gameTypes":   env.gameTypes,
			"tickMs":      env.tickMS,
			"jackpotInit": env.jackpotInit,
		},
	}); err != nil {
		log.Fatalf("race-generator: audit init: %v", err)
	}

	// Construir runners por gameType (orden estable = orden de env.gameTypes).
	runners, err := buildRunners(env.gameTypes, env.jackpotInit)
	if err != nil {
		log.Fatalf("race-generator: build runners: %v", err)
	}
	for _, r := range runners {
		log.Printf("race-generator: runner ready gameType=%s betoffer=%d interval=%ds pool=%d entries",
			r.cfg.GameType, r.cfg.BetofferID, r.cfg.RoundIntervalSec, r.sel.Len())
	}

	// Horizon depth from env (default 25). Set BEFORE bootBackfill/bootBulk
	// and before the scheduler loop (tickHorizon reads the same var).
	bulkSize = env.horizon

	// Boot is two phases, both SYNCHRONOUS (the "ready" line below is
	// race-broadcaster's signal that the GA window is full and safe to
	// query — a goroutine'd boot could let a fast-starting broadcaster read
	// an empty/underfilled pool, cold-start race per 03-RACE-BROADCASTER.md
	// Riesgo #2):
	//
	//  1. bootBackfill — the current in-progress round + bootBackfillPast
	//     finished rounds (cur-N..cur). Guarantees finished games exist
	//     immediately (no cold-start crash) and the timeline is contiguous
	//     down into the recent past (no Race Break gap on the history side).
	//  2. bootBulk — the future horizon (cur+1..cur+bulkSize).
	//
	// Both are idempotent: on a warm restart the slots already in the DB are
	// upsert-skipped, only genuinely-missing slots are (re)generated, so a
	// short outage (< bulkSize intervals) is fully self-covered by the buffer.
	now := time.Now()
	if err := bootBackfill(runners, mt, aud, now, bootBackfillPast); err != nil {
		log.Fatalf("race-generator: bootBackfill: %v", err)
	}
	if err := bootBulk(runners, mt, aud, now, bulkSize); err != nil {
		log.Fatalf("race-generator: bootBulk: %v", err)
	}
	for _, r := range runners {
		log.Printf("race-generator: boot bulk OK gameType=%s rounds=%d lastSlot=%s",
			r.cfg.GameType, bulkSize, r.lastSlot.Format(time.RFC3339))
	}
	log.Printf("race-generator ready: %d future rounds per gameType pre-generated", bulkSize)

	// Signal handling -> cancel ctx -> scheduler returns -> defers run.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Printf("race-generator: scheduler start tickMs=%d gameTypes=%v", env.tickMS, env.gameTypes)
	runScheduler(ctx, runners, mt, aud, time.Duration(env.tickMS)*time.Millisecond)
	log.Printf("race-generator: scheduler stopped, shutting down")
}

// buildRunners is the single source of truth for gameTypeRunner
// construction — invoked once from main() and once per integration test
// in cmd/race-generator/main_test.go. Returns runners in the same order
// as the input slice (orden estable = orden de env.gameTypes).
func buildRunners(gameTypes []string, jackpotInit float64) ([]*gameTypeRunner, error) {
	runners := make([]*gameTypeRunner, 0, len(gameTypes))
	for _, gt := range gameTypes {
		cfg, err := config.Get(gt)
		if err != nil {
			return nil, fmt.Errorf("unknown game type %q: %w", gt, err)
		}
		pool := data.VideoPool(cfg.VideoPoolPath)
		if pool == nil {
			return nil, fmt.Errorf("data.VideoPool(%q) returned nil", cfg.VideoPoolPath)
		}
		sel, err := videoselector.New(pool, cfg)
		if err != nil {
			return nil, fmt.Errorf("videoselector.New(%s): %w", gt, err)
		}
		runners = append(runners, &gameTypeRunner{
			cfg:         cfg,
			sel:         sel,
			jackpot:     &generators.JackpotState{Current: jackpotInit},
			recentNames: newNameCooldown(cfg.NumberCompetitor * 10),
			// lastSlot = zero => bootBulk's first persisted slot becomes
			// the new lastSlot, then advances one slot per crossed tick.
		})
	}
	return runners, nil
}

// bootBulk pre-generates `n` future rounds for each runner SYNCHRONOUSLY.
// Caller must ensure SQLite + audit log are already initialized.
//
// Each round flows through the same generateAndPersist pipeline used at
// tick-time, so the audit chain laid down here is indistinguishable
// from later tick output (init → 10*(game_generated, state_mod) per
// gameType for a 2-game-type binary = 1 + 2*N*len(runners) total entries
// after boot).
//
// On error, the function returns immediately — partial state stays in
// the DB and audit log (the audit chain is intact up to the failure
// point), and the caller decides whether to abort the binary. Today
// main() calls log.Fatalf, which is the safest option: a partial boot
// would leave race-broadcaster reading an underfilled window.
//
// SYNCHRONOUS by design: see main()'s "ready" log invariant in
// 03-RACE-BROADCASTER.md (Riesgo #2 mitigation).
func bootBulk(runners []*gameTypeRunner, mt rng.Source, aud *audit.Log, now time.Time, n int) error {
	for _, r := range runners {
		for i := 0; i < n; i++ {
			slot := r.scheduledSlot(now, i)
			if err := generateAndPersist(r, slot, mt, aud); err != nil {
				return fmt.Errorf("bootBulk %s slot=%s: %w",
					r.cfg.GameType, slot.Format(time.RFC3339), err)
			}
			r.lastSlot = slot
		}
	}
	return nil
}

// bootBackfill generates, for each runner, the current in-progress round
// plus `pastCount` finished rounds immediately before it — slots
// cur-pastCount .. cur — SYNCHRONOUSLY, before bootBulk lays down the
// future horizon.
//
// Why this is needed: bootBulk only generates strictly-future slots
// (cur+1 ..). On a steady-state running generator the current/past slots
// already exist (they were future once), but on a COLD START (empty DB) or
// after an outage longer than the buffer they are missing — leaving the /tv
// pool with no finished round to render (attachVideoScreen cold-start crash)
// or a gap in the served window [N-1,N,N+1,N+2] (Race Break). Generating the
// recent past here guarantees a contiguous timeline.
//
// scheduledSlot(now, k) maps k=-1 → cur (in-progress), k=-2 → cur-1, …,
// so we walk k from -(pastCount+1) up to -1 to emit cur-pastCount .. cur in
// chronological order. Idempotent: generateAndPersist→UpsertGameRound skips
// already-finished rounds (returns written=false), so a warm restart is a
// no-op here. Does NOT touch r.lastSlot — that is the future-horizon edge,
// owned by bootBulk/tickHorizon; the backfilled past slots are behind it.
func bootBackfill(runners []*gameTypeRunner, mt rng.Source, aud *audit.Log, now time.Time, pastCount int) error {
	for _, r := range runners {
		for k := -(pastCount + 1); k <= -1; k++ {
			slot := r.scheduledSlot(now, k)
			if err := generateAndPersist(r, slot, mt, aud); err != nil {
				return fmt.Errorf("bootBackfill %s slot=%s: %w",
					r.cfg.GameType, slot.Format(time.RFC3339), err)
			}
		}
	}
	return nil
}

// runScheduler is the main loop. On each tick it delegates to
// tickHorizon, which walks the runners in declaration order and emits
// at most ONE new round per runner — the slot at the far end of the
// horizon (bulkSize intervals into the future) IF that slot has not
// already been persisted. Errors are logged inside tickRunner and the
// next tick continues — the binary does NOT terminate on per-round
// failures (regulators expect continuous operation; a single transient
// error must not stop the scheduler).
func runScheduler(
	ctx context.Context,
	runners []*gameTypeRunner,
	mt rng.Source,
	aud *audit.Log,
	tick time.Duration,
) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			tickHorizon(runners, mt, aud, now)
		}
	}
}

// tickHorizon is the body executed once per ticker tick. Extracted so
// the test suite can drive boundary crossings deterministically without
// spinning a real time.Ticker.
//
// For each runner, it computes the slot at the far edge of the horizon
// (nextFutureSlot(now, intervalSec, bulkSize-1)) and persists it ONLY
// if it is strictly newer than r.lastSlot. Because the boot bulk has
// already populated slots 0..bulkSize-1 and ticks happen far more often
// than intervalSec (default tick = 1s, interval = 240s), most ticks
// are no-ops: only the tick that first crosses a new boundary advances
// the horizon by one round.
func tickHorizon(runners []*gameTypeRunner, mt rng.Source, aud *audit.Log, now time.Time) {
	for _, r := range runners {
		slot := r.scheduledSlot(now, bulkSize-1)
		if !slot.After(r.lastSlot) {
			continue
		}
		tickRunner(r, slot, mt, aud)
	}
}

// tickRunner runs one slot of one runner with full panic isolation.
//
// Wrapping the per-runner body in an immediately-invoked function with
// `defer recover()` ensures that a panic anywhere in the pipeline
// (GenerateGame, adapter.ToGameRound, sqlite.UpsertGameRound, ...) does
// NOT crash the whole binary. The scheduler's docstring promises
// "continuous operation" — a single panic on one runner must not stop
// the scheduler from processing the other runners or future ticks.
//
// Panics are treated as PERMANENT class: we log the runner, slot, and
// stack trace, then advance lastSlot so we don't re-trigger the same
// crash on the next tick. The audit log already has the partial state
// up to the panic point (e.g. game_generated emitted, panic during
// persist) — that's preserved for postmortem.
//
// EXCEPTION — rng.EntropyError is NOT recovered: an entropy failure means
// the game must STOP (GLI-19 R11 fail-safe). Swallowing it here would keep
// the scheduler "healthy" while silently producing no rounds — the inverse
// of fail-closed. We terminate the process instead.
func tickRunner(r *gameTypeRunner, slot time.Time, mt rng.Source, aud *audit.Log) {
	defer func() {
		if rec := recover(); rec != nil {
			if ee, ok := rec.(rng.EntropyError); ok {
				log.Fatalf("race-generator: FATAL entropy failure (GLI-19 R11 fail-closed) %s slot=%s: %v",
					r.cfg.GameType, slot.UTC().Format(time.RFC3339), ee)
			}
			log.Printf("race-generator: PANIC %s slot=%s: %v\n%s",
				r.cfg.GameType, slot.UTC().Format(time.RFC3339),
				rec, debug.Stack())
			r.lastSlot = slot
			r.retryCount = 0
		}
	}()
	if err := generateAndPersist(r, slot, mt, aud); err != nil {
		// Error classification:
		//
		// - PERMANENT / give-up: shape-check failures from GenerateGame
		//   (len mismatches, invalid finish indices), adapter.ToGameRound
		//   errors. These indicate a bug or malformed config — retrying
		//   the same slot will fail again next tick.
		//
		// - TRANSIENT / retryable: I/O errors against the audit log
		//   (disk-full, EIO) and SQLite errors that surface "database
		//   is locked" / "disk is full" / busy-timeout-exceeded
		//   conditions. The underlying condition usually clears on its
		//   own within seconds, so we keep lastSlot pinned and let the
		//   next tick retry.
		//
		// Bound the retry: after 3 consecutive transient failures on
		// the same slot we give up and advance lastSlot anyway,
		// preventing an infinite hot-loop.
		if isTransientErr(err) && r.retryCount < 3 {
			r.retryCount++
			log.Printf("race-generator: %s slot=%s transient err (retry %d/3): %v",
				r.cfg.GameType, slot.UTC().Format(time.RFC3339),
				r.retryCount, err)
			return
		}
		log.Printf("race-generator: %s slot=%s permanent err (advancing lastSlot): %v",
			r.cfg.GameType, slot.UTC().Format(time.RFC3339), err)
		r.lastSlot = slot
		r.retryCount = 0
		return
	}
	r.lastSlot = slot
	r.retryCount = 0
}

// isTransientErr returns true if err looks like a recoverable I/O or
// SQLite condition that warrants retrying the same slot on the next
// tick rather than skipping it forever.
//
// Heuristic checklist:
//   - syscall.ENOSPC / "disk is full" / "no space left on device" —
//     audit log or DB ran out of disk; admin will free space.
//   - os.ErrPermission — temporary FS lockout (e.g. mid-rotation).
//   - SQLite "database is locked" / "SQLITE_BUSY" — modernc.org/sqlite
//     surfaces busy beyond the busy_timeout pragma as a stringly-typed
//     error. We don't import the driver's err constants to avoid a hard
//     coupling; string-match the canonical message instead.
//
// All other errors (shape-check failures, malformed config, panics
// caught by recover) are treated as permanent.
func isTransientErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EIO) {
		return true
	}
	msg := err.Error()
	for _, needle := range []string{
		"database is locked",
		"SQLITE_BUSY",
		"disk is full",
		"no space left on device",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// nextFutureSlot returns the slot boundary `n` intervals strictly into
// the future from `now`, aligned to multiples of intervalSec measured
// against unix-epoch UTC.
//
// Concretely:
//
//	d    := time.Duration(intervalSec) * time.Second
//	base := now.UTC().Truncate(d)
//	return base.Add(time.Duration(n+1) * d)
//
// So n=0 is the nearest boundary strictly after `now` (i.e. when `now`
// is already on a boundary, n=0 returns `now + d`, NOT `now` itself).
// n=9 with bulkSize=10 is the far edge of the boot/horizon window.
//
// Plan 02 Task 2 replaced the legacy nextSlot — which returned the most
// recent PAST boundary — with this future-oriented variant so that
// every persisted GA round has videoStartDt > now at the moment it is
// written. The /tv handler in internal/protocol/protocol.go relies on
// `now > videoEndDt` to gate the `finish` payload; with past-slotted
// rounds the gate would never engage and a round would appear to TV
// clients as if it had already finished.
func nextFutureSlot(now time.Time, intervalSec int, n int) time.Time {
	if intervalSec <= 0 {
		return now.UTC().Truncate(time.Second)
	}
	d := time.Duration(intervalSec) * time.Second
	base := now.UTC().Truncate(d)
	return base.Add(time.Duration(n+1) * d)
}

// scheduledSlot returns the slot `n` races strictly into the future from
// `now`, aligned to raceutil's per-game-type schedule grid (NOT the unix
// epoch that nextFutureSlot truncates to). This is the second half of the
// idRace-epoch fix: it makes every generated round's videoStartDt land on
// exactly the same instant the DS vendor (and raceutil) place that race, so
// a GA round and the DS round for the same wall-clock race carry the same
// number AND start at the same time.
//
// raceutil.VideoStartTime(gameType, num, now) = schedStart(now) + (num-1)*interval.
// CurrentRaceNumber(now) is the in-progress race, so +1 is the next
// strictly-future race and +n walks the horizon.
//
// IMPORTANT — this function intentionally passes raceutil a race number that
// may NOT exist on `now`'s schedule day. Near the end of a day cur+1+n can
// exceed the day's race count (e.g. dog8 361 when only 360 fit), because we
// always anchor VideoStartTime to schedStart(now). VideoStartTime does
// unbounded linear arithmetic, so that out-of-range number still yields a real
// instant — and because the interval evenly divides 24h (dog8/dog6 240s → 360
// races/day; horse 320s → 270), that instant lands EXACTLY on the next
// schedule day's grid. We do NOT rely on the number itself being valid: the
// returned value is only a *slot* (an instant). GenerateGame then recomputes
// the authoritative number/date/code from that slot via raceutil
// (CurrentRoundCode(slot)), which anchors to the slot's own schedule day. So
// the identity self-corrects across the boundary with no special-casing here.
// Do NOT "fix" the apparent overflow by clamping cur+1+n to the day count —
// that would desync the slot from the grid and reintroduce missed lookups.
//
// Fallback: if raceutil has no schedule for this game type (should never
// happen for the supported set — guard only), fall back to the legacy
// unix-aligned grid so the scheduler keeps running rather than stalling.
func (r *gameTypeRunner) scheduledSlot(now time.Time, n int) time.Time {
	cur := raceutil.CurrentRaceNumber(r.cfg.GameType, now)
	if cur <= 0 {
		return nextFutureSlot(now, r.cfg.RoundIntervalSec, n)
	}
	return raceutil.VideoStartTime(r.cfg.GameType, cur+1+n, now)
}

// generateAndPersist runs the per-round pipeline:
//  1. GenerateGame (emits `game_generated` internally — do NOT duplicate).
//  2. ModifyStateBetweenGames -> audit `state_mod` (cycles MT for the
//     NEXT round; this is the GLI-19 §3.2.6 "background cycling between
//     games" step. Semantically it sits AFTER a successful round so the
//     audit chain reads init → game_generated → state_mod → game_generated
//     → state_mod → … with state_mod entries sitting strictly between
//     consecutive games. If we put state_mod BEFORE GenerateGame and the
//     latter failed, the chain would contain a phantom state_mod with
//     no matching game_generated — that's what we're now avoiding.).
//  3. recentNames cooldown bookkeeping.
//  4. adapter.ToGameRound.
//  5. sqlite.UpsertGameRound + sqlite.SaveResult.
//
// Any error short-circuits and is returned to the caller (the scheduler
// classifies and decides whether to retry the same slot). The audit log
// is best-effort beyond the GenerateGame call — GenerateGame returns its
// own audit failure as an error so corrupt audit chains cannot mask game
// emission.
func generateAndPersist(
	r *gameTypeRunner,
	slot time.Time,
	mt rng.Source,
	aud *audit.Log,
) error {
	gameID := fmt.Sprintf("%s-%s", r.cfg.GameType, slot.UTC().Format("20060102T150405Z"))

	// 1. Single round. GenerateGame emits its own `game_generated` entry
	// (do NOT re-emit `round_generated` here — would double-write).
	g, err := generators.GenerateGame(mt, r.cfg, r.sel, r.jackpot, slot, r.recentNames.Excludes(), aud)
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}

	// 2. Round transition AFTER this round (GLI-19 §3.2.6 background cycling
	// + DRBG reseed) via the SHARED helper rng.BetweenRounds — the same
	// sequence the GLI extraction harness executes, so "same RNG and
	// methods" is structural, not aspirational. Production always passes a
	// Reseeder (makeSource returns rng.CertifiedStream — compile-time
	// guarantee); only tests inject plain Sources, in which case the reseed
	// step and its audit field are omitted. Emit audit ONLY on success — we
	// never want a state_mod entry that doesn't correspond to a real
	// round-to-round transition.
	rs, _ := mt.(rng.Reseeder)
	sm, err := rng.BetweenRounds(mt, rs, gameID)
	if err != nil {
		return fmt.Errorf("round transition: %w", err)
	}
	// genBefore/genAfter make the FULL stream consumption reconcilable from
	// the audit log: game_generated.mtSeqAfter → state_mod.genBefore covers
	// the discard-count extraction draw(s); genBefore→genAfter is the
	// discard itself. No unexplained draws between consecutive rounds.
	payload := map[string]any{
		"gameId":    sm.GameID,
		"reason":    sm.Reason,
		"discard":   sm.DiscardCount,
		"genBefore": sm.GenBefore,
		"genAfter":  sm.GenAfter,
	}
	if rs != nil {
		payload["reseeds"] = rs.ReseedCount()
	}
	if err := aud.Append(audit.Entry{Kind: "state_mod", Payload: payload}); err != nil {
		return fmt.Errorf("audit state_mod: %w", err)
	}

	// 3. Cooldown: push this round's names onto the FIFO ring. Capacity
	// (NumberCompetitor*10) matches the legacy 10-round anti-repetition
	// window. Names exceeding the window are evicted oldest-first,
	// becoming eligible again one-by-one (NOT all-at-once like the
	// previous wholesale-wipe strategy).
	for _, c := range g.Competitors {
		r.recentNames.Add(c.Name)
	}

	// 4. Project to persistence shape.
	gr, results, err := adapter.ToGameRound(g, r.cfg)
	if err != nil {
		return fmt.Errorf("adapter: %w", err)
	}

	// 5. Persist. UpsertGameRound returns written=false for skipped writes
	// (e.g. already-finished rounds) — log but do not treat as error.
	written, err := sqlite.UpsertGameRound(gr)
	if err != nil {
		return fmt.Errorf("upsert %s: %w", gr.RoundCode, err)
	}
	if !written {
		log.Printf("race-generator: upsert skipped (already finished) round=%s", gr.RoundCode)
	}
	if err := sqlite.SaveResult(gr.RoundCode, results); err != nil {
		return fmt.Errorf("save result %s: %w", gr.RoundCode, err)
	}

	if written {
		log.Printf("race-generator: emitted round=%s gameType=%s 1st=%d 2nd=%d bonus=%d",
			gr.RoundCode, gr.GameType, g.Finish.First, g.Finish.Second, g.Bonus)
	}
	return nil
}

// The SP 800-90A personalization string lives single-sourced in the rng
// package (rng.PersonalizationV1) so this binary and the GLI extraction
// harness cannot drift apart.

// loadEnv reads and validates the binary's environment variables. All
// defaults live here (single source of truth — see the header comment).
func loadEnv() (envConfig, error) {
	e := envConfig{
		dbPath:      getenvDefault("DB_PATH", "./data/relay.db"),
		seedHex:     os.Getenv("RACEGEN_SEED_HEX"),
		auditPath:   getenvDefault("RACEGEN_AUDIT_PATH", "./data/racegen-audit.jsonl"),
		jackpotInit: generators.JackpotInitialValue,
		tickMS:      1000,
		horizon:     25,
	}

	// Horizon override (optional). Min 2 — the webview needs 2 future
	// rounds; below that the /tv pool short-loads and Race-Breaks.
	if v := os.Getenv("RACEGEN_HORIZON"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return envConfig{}, fmt.Errorf("RACEGEN_HORIZON %q: %w", v, err)
		}
		if n < 2 {
			return envConfig{}, fmt.Errorf("RACEGEN_HORIZON %d: must be >= 2", n)
		}
		e.horizon = n
	}

	// Game types — comma-separated, trimmed, deduped (preserving first-seen order).
	raw := os.Getenv("RACEGEN_GAMETYPES")
	if raw == "" {
		e.gameTypes = config.SupportedGameTypes()
	} else {
		seen := make(map[string]bool)
		for _, p := range strings.Split(raw, ",") {
			p = strings.TrimSpace(p)
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			e.gameTypes = append(e.gameTypes, p)
		}
		if len(e.gameTypes) == 0 {
			return envConfig{}, errors.New("RACEGEN_GAMETYPES set but empty after parse")
		}
	}

	// Jackpot init override (optional).
	if v := os.Getenv("RACEGEN_JACKPOT_INIT"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return envConfig{}, fmt.Errorf("RACEGEN_JACKPOT_INIT %q: %w", v, err)
		}
		if f < 0 {
			return envConfig{}, fmt.Errorf("RACEGEN_JACKPOT_INIT %g: must be >= 0", f)
		}
		e.jackpotInit = f
	}

	// Tick override (optional).
	if v := os.Getenv("RACEGEN_TICK_MS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return envConfig{}, fmt.Errorf("RACEGEN_TICK_MS %q: %w", v, err)
		}
		if n < 1 {
			return envConfig{}, fmt.Errorf("RACEGEN_TICK_MS %d: must be >= 1", n)
		}
		e.tickMS = n
	}

	// Seed sanity: if provided, must be exactly 64 hex chars AND decode
	// cleanly. Validating the content here (BEFORE we open the audit
	// file or the DB) avoids leaving an orphan empty audit.jsonl behind
	// when the seed is malformed — previously the length check passed
	// for a 64-char garbage seed and the hex-decode failure surfaced
	// inside rng.NewMT19937WithSeedHex AFTER audit.Open had created the
	// file.
	if e.seedHex != "" {
		if len(e.seedHex) != 64 {
			return envConfig{}, fmt.Errorf("RACEGEN_SEED_HEX must be 64 hex chars, got %d", len(e.seedHex))
		}
		if _, err := hex.DecodeString(e.seedHex); err != nil {
			return envConfig{}, fmt.Errorf("RACEGEN_SEED_HEX invalid hex: %w", err)
		}
	}
	// NOTE: whether a seed is allowed at all is decided per build by
	// makeSource (source_prod.go / source_lab.go). Production builds REJECT
	// any seed (GLI-19: production seeding must be unpredictable); gli_lab
	// builds REQUIRE one. The previous fail-closed rule ("prod requires a
	// seed for audit replay") inverted the standard and was finding H2 of
	// docs/AUDITORIA-RNG-GLI19.md.

	return e, nil
}

// getenvDefault returns os.Getenv(k) if non-empty, otherwise def.
func getenvDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
