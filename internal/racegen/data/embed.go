// Package data exposes the embedded reference assets used by the racegen
// generator: the dog-name pool and the video-finish pools (dog8 / dog6 /
// horse_classic).
//
// All four JSON files are embedded into the binary via go:embed and parsed
// lazily on first use (sync.Once per asset). Parsed structures are
// effectively immutable — callers receive shared slices, do not mutate.
//
// SOURCE AUDIT (Phase 2, 2026-05-17):
//
//	names.json
//	  source: virteon-platform/packages/game-engine/src/data/names.json
//	  sha256: afde2ba71ead7258f45d139ca327df350e45bc9cf8276896dd926f4a5eea0e1f
//	  size:   16314 bytes, 1424 string entries
//
//	videoResults-dog8.json
//	  source: virteon-platform/packages/game-engine/src/data/videoResults.json
//	  sha256: 396c4a56d19673778ef41465f6d5fb6211bc1cf6b9026683797f261022fca2c1
//	  size:   47679 bytes, 411 entries keyed DOG8#001..DOG8#411,
//	          each value an 8-position finish map {"1":int .. "8":int}.
//	          File was renamed on copy from videoResults.json to
//	          videoResults-dog8.json for naming symmetry with dog6.
//
//	videoResults-dog6.json
//	  source: virteon-platform/data/video-pools/videoResults-dog6.json
//	  sha256: 43be12c3f307f7d75d833baa4f307f1a451c4f557c5c234864a4e6ca8f629cfc
//	  size:   90078 bytes, 979 entries keyed DOG6#001..DOG6#979,
//	          each value a 6-position finish map.
//	          NB: We deliberately import this from data/video-pools/ (the
//	          full 6-position file), NOT from packages/game-engine/src/data/
//	          (which is lossy — only positions 1 and 2). Phase 4 adapter
//	          needs positions 3-6.
//
//	videoResults-horse_classic.json   (added 2026-06-09, rebuilt from real videos)
//	  source: the REAL horse video files under ds_assets/DSVideo/horse/*.mp4
//	          (downloaded from the DS vendor by ds-tools/download_horse_for_mac.py
//	          for MAC 00:1E:06:48:69:E5). For horse_classic the video FILENAME IS
//	          the finishing order: a 7-digit permutation of runners 1..7 where
//	          digit i = the runner that finished in position i (e.g. "1237465" →
//	          pos1=1, pos2=2, ..., pos7=5 → file 1237465.mp4). Keyed by that
//	          7-digit stem; the value is the full 7-position finish decoded from
//	          the key. 338 distinct clean videos (20 non-perm variants/intros
//	          dropped). This is the GLI-correct pairing: the file shown IS the
//	          finish/payout. (The relay.db GameRounds.VideoName↔GameResults
//	          pairing is misaligned — same as dogs — so it is NOT the source.)
//	  decode cross-check: 338/338 keys match virteon-platform
//	          packages/game-engine/src/data/videoResults-horse7.json positions 1-2.
//	  sha256: e1eba2bf019b7c61f154bcf69a17153659ed002b5179b003366a5fde114861bd
//	  size:   18253 bytes, 338 entries.
//	  NB: winner (pos-1) box distribution is near-uniform (~14.0–15.1% per box,
//	      expected 1/7≈14.28%), consistent with the DS reference (doc 08: DS
//	      plays ~uniform). The IPF videoselector re-weights toward the config's
//	      TargetFirstPlace/SecondPlace at use site, so pool size/multiplicity
//	      only sets the spanned permutation space.
package data

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"vg-racegen/internal/racegen/rng"
)

//go:embed names.json
var rawNames []byte

//go:embed videoResults-dog8.json
var rawDog8 []byte

//go:embed videoResults-dog6.json
var rawDog6 []byte

//go:embed videoResults-horse_classic.json
var rawHorseClassic []byte

// VideoFinish is one entry of a video pool. Order is the runner numbers
// at finish positions 1..N (1-based), length == NumberCompetitor for
// the pool's game type (8 for dog8, 6 for dog6, 7 for horse_classic).
//
// IMPORTANT: do not mutate Order. The Pool returns shared slices.
type VideoFinish struct {
	ID    string
	Order []int
}

// Pool is the in-memory representation of one videoResults JSON file.
// Treat as immutable. Pool.Entries is sorted by ID (lex) for
// deterministic indexing — load order from the JSON map is not stable.
type Pool struct {
	GameType string
	NumComp  int
	Entries  []VideoFinish
	byID     map[string]int
}

// Len returns the number of entries in the pool.
func (p *Pool) Len() int { return len(p.Entries) }

// At returns the i-th entry (0-indexed). Panics if i is out of range.
func (p *Pool) At(i int) VideoFinish { return p.Entries[i] }

// ByID looks up an entry by its string ID (e.g. "DOG8#017").
func (p *Pool) ByID(id string) (VideoFinish, bool) {
	idx, ok := p.byID[id]
	if !ok {
		return VideoFinish{}, false
	}
	return p.Entries[idx], true
}

// ----------------------------------------------------------------------------
// Lazy parsing
// ----------------------------------------------------------------------------

var (
	namesOnce sync.Once
	names     []string

	dog8Once sync.Once
	dog8Pool *Pool

	dog6Once sync.Once
	dog6Pool *Pool

	horseClassicOnce sync.Once
	horseClassicPool *Pool
)

// DogNames returns the embedded name list (cached). Caller must NOT mutate.
// For shuffled access, copy the slice and run rng.CertifiedShuffle on the copy.
func DogNames() []string {
	namesOnce.Do(func() {
		if err := json.Unmarshal(rawNames, &names); err != nil {
			panic(fmt.Sprintf("data: failed to parse names.json: %v", err))
		}
	})
	return names
}

// VideoPool returns the pool for "dog8", "dog6", or "horse_classic", or nil
// otherwise. Lazy-parsed via sync.Once per game type. First call parses
// (~1ms); subsequent calls are O(1). Concurrent-safe.
func VideoPool(gameType string) *Pool {
	switch gameType {
	case "dog8":
		dog8Once.Do(func() {
			dog8Pool = parsePool("dog8", 8, rawDog8)
		})
		return dog8Pool
	case "dog6":
		dog6Once.Do(func() {
			dog6Pool = parsePool("dog6", 6, rawDog6)
		})
		return dog6Pool
	case "horse_classic":
		horseClassicOnce.Do(func() {
			horseClassicPool = parsePool("horse_classic", 7, rawHorseClassic)
		})
		return horseClassicPool
	default:
		return nil
	}
}

// parsePool decodes a videoResults JSON blob into a Pool with entries
// sorted lexicographically by ID.
//
// Position keys in the source JSON are 1-based string integers. For most
// entries they are exactly "1".."N" — but a small minority of dog6 entries
// (6/979 in the live production export) use sparse position labels (e.g.
// {"1","2","3","4","7","8"}) where the underlying ES gameResult docs
// recorded sparse finish slots. All N runners are always present, the
// label gaps are an artifact of the source system.
//
// We normalize by sorting position-key integers ascending and remapping to
// dense finish positions 1..N. The order of runners across the finish
// line is preserved regardless of label compaction.
func parsePool(gameType string, numComp int, raw []byte) *Pool {
	var src map[string]map[string]int
	if err := json.Unmarshal(raw, &src); err != nil {
		panic(fmt.Sprintf("data: failed to parse videoResults-%s.json: %v", gameType, err))
	}

	// Collect keys and sort lexicographically so the pool is deterministic
	// regardless of the JSON object's iteration order.
	ids := make([]string, 0, len(src))
	for k := range src {
		ids = append(ids, k)
	}
	sort.Strings(ids)

	entries := make([]VideoFinish, 0, len(ids))
	byID := make(map[string]int, len(ids))
	for i, id := range ids {
		posMap := src[id]
		if len(posMap) != numComp {
			panic(fmt.Sprintf("data: %s entry %s has %d positions, want %d",
				gameType, id, len(posMap), numComp))
		}

		// Parse position keys, sort numerically, then assign runners to
		// dense positions 1..N in that order.
		type kv struct {
			pos    int
			runner int
		}
		pairs := make([]kv, 0, numComp)
		for posStr, runner := range posMap {
			var pos int
			if _, err := fmt.Sscanf(posStr, "%d", &pos); err != nil {
				panic(fmt.Sprintf("data: %s entry %s has non-integer position key %q",
					gameType, id, posStr))
			}
			if runner < 1 || runner > numComp {
				panic(fmt.Sprintf("data: %s entry %s runner %d out of [1,%d]",
					gameType, id, runner, numComp))
			}
			pairs = append(pairs, kv{pos: pos, runner: runner})
		}
		sort.Slice(pairs, func(a, b int) bool { return pairs[a].pos < pairs[b].pos })

		order := make([]int, numComp)
		seen := make(map[int]struct{}, numComp)
		for densePos, p := range pairs {
			if _, dup := seen[p.runner]; dup {
				panic(fmt.Sprintf("data: %s entry %s duplicate runner %d", gameType, id, p.runner))
			}
			seen[p.runner] = struct{}{}
			order[densePos] = p.runner
		}
		entries = append(entries, VideoFinish{ID: id, Order: order})
		byID[id] = i
	}

	return &Pool{
		GameType: gameType,
		NumComp:  numComp,
		Entries:  entries,
		byID:     byID,
	}
}

// ----------------------------------------------------------------------------
// Stateless single-draw helpers
// ----------------------------------------------------------------------------

// PickName is a stateless single uniform draw. Cooldown logic lives in
// generators/competitors.go (Phase 3 — out of scope here).
func PickName(mt *rng.MT19937) string {
	pool := DogNames()
	if len(pool) == 0 {
		panic("data: empty name pool")
	}
	idx := rng.CertifiedInt(mt, 0, len(pool)-1)
	return pool[idx]
}

// PickVideo is a stateless single uniform draw. Phase 3 IPF selector
// builds a weighted selector on top of Pool.Entries.
// Returns (zero, false) if gameType is unknown.
func PickVideo(mt *rng.MT19937, gameType string) (VideoFinish, bool) {
	p := VideoPool(gameType)
	if p == nil || p.Len() == 0 {
		return VideoFinish{}, false
	}
	idx := rng.CertifiedInt(mt, 0, p.Len()-1)
	return p.At(idx), true
}
