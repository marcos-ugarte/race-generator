package generators

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"vg-racegen/internal/racegen/config"
	"vg-racegen/internal/racegen/data"
	"vg-racegen/internal/racegen/rng"
	"vg-racegen/internal/racegen/videoselector"
)

// TestFinishConsistencyWithVideoPool guarantees the user-mandated invariant:
// for every generated round, the finish positions match the runner order
// recorded in the video pool entry for the selected video. In operational
// terms: the video the TV physically reproduces (R0xxx.mp4) shows the
// runners crossing the line in EXACTLY the same order that the results
// screen displays.
//
// Why this test exists: a regression in either videoselector.Select (Order
// truncated/reshuffled), GenerateFinish (positions filled out of sync with
// pick.Order), or buildVideoName (R-number drift from pool ID) would silently
// produce an inconsistent broadcast — the spectator would see runner #3
// cross first but the results panel would crown a different competitor.
// The bug would be invisible to any unit test that only checks shape and
// to any smoke test that doesn't reverse-derive the pool entry from the
// persisted videoname.
//
// Empirical validation: 5 samples from smoke v5 DB on 2026-05-19 matched
// pool order 5/5 (see docs/racegen-design/02-ARCHITECTURE.md). This test
// automates the same check across 50 deterministic seeds per game type so
// future drift fails CI.
func TestFinishConsistencyWithVideoPool(t *testing.T) {
	rPattern := regexp.MustCompile(`R(\d+)`)

	for _, gt := range []string{"dog8", "dog6"} {
		t.Run(gt, func(t *testing.T) {
			cfg, err := config.Get(gt)
			if err != nil {
				t.Fatalf("config.Get(%s): %v", gt, err)
			}
			pool := data.VideoPool(cfg.VideoPoolPath)
			if pool == nil {
				t.Fatalf("data.VideoPool(%s) returned nil", cfg.VideoPoolPath)
			}
			sel, err := videoselector.New(pool, cfg)
			if err != nil {
				t.Fatalf("videoselector.New: %v", err)
			}

			poolPrefix := fmt.Sprintf("DOG%d", cfg.NumberCompetitor)

			const samples = 50
			for i := 0; i < samples; i++ {
				seedHex := fmt.Sprintf("%064x", i+1)
				mt, err := rng.NewMT19937WithSeedHex(seedHex)
				if err != nil {
					t.Fatalf("seed %d: %v", i, err)
				}
				f := GenerateFinish(mt, cfg, sel)

				// Reverse-derive the pool ID from the persisted videoname.
				// buildVideoName encodes pool ID "DOG8#144" as "R0144.mp4";
				// this regex inverts that transform.
				m := rPattern.FindStringSubmatch(f.VideoName.MP4)
				if len(m) < 2 {
					t.Fatalf("seed %d: cannot extract R-number from %q", i, f.VideoName.MP4)
				}
				num, err := strconv.Atoi(m[1])
				if err != nil {
					t.Fatalf("seed %d: bad R-number %q: %v", i, m[1], err)
				}
				poolID := fmt.Sprintf("%s#%03d", poolPrefix, num)

				entry, ok := pool.ByID(poolID)
				if !ok {
					t.Fatalf("seed %d: poolID %q not in pool (video=%s)",
						i, poolID, f.VideoName.MP4)
				}

				// Compare finish["k"].CompetitorIndex against entry.Order[k-1].
				for k := 1; k <= cfg.NumberCompetitor; k++ {
					fp, ok := f.Finish[strconv.Itoa(k)]
					if !ok {
						t.Fatalf("seed %d: missing finish position %d", i, k)
					}
					want := entry.Order[k-1]
					if fp.CompetitorIndex != want {
						t.Errorf(
							"seed %d pos %d: got runner %d, want %d (poolID=%s, video=%s, fullPoolOrder=%v)",
							i, k, fp.CompetitorIndex, want, poolID,
							strings.TrimPrefix(f.VideoName.MP4, "/.local/"),
							entry.Order,
						)
					}
				}
			}
		})
	}
}
