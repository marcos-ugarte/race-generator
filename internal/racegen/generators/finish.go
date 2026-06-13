package generators

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"vg-racegen/internal/racegen/config"
	"vg-racegen/internal/racegen/rng"
	"vg-racegen/internal/racegen/videoselector"
)

// FinishData is the canonical finish/interval/video payload for one race.
// Field tags match the legacy adapter shape (finish.ts:46-52).
type FinishData struct {
	Finish    map[string]FinishPosition              `json:"finish"`
	Interval  map[string]map[string]IntervalPosition `json:"interval"`
	VideoName VideoName                              `json:"videoname"`
	First     int                                    `json:"first"`
	Second    int                                    `json:"second"`

	// order is the 1-based slot list in finish-rank order (order[0] is the
	// first-place runner). It is unexported so it does NOT serialize — it
	// is an internal hand-off to GenerateOdds for the odds↔finish coupling
	// (see Order accessor and GenerateOdds). Mirrors the selected pool
	// entry's Order.
	order []int
}

// FinishPosition is one entry of the Finish map. Time is a pointer so
// positions 3..N can serialize as JSON `null` (matches the legacy enriched
// payload — only the first two positions carry a clock time).
type FinishPosition struct {
	CompetitorIndex int      `json:"competitorIndex"`
	Time            *float64 `json:"time"`
}

// IntervalPosition is one rank within a single interval checkpoint.
type IntervalPosition struct {
	CompetitorIndex int     `json:"competitorIndex"`
	Time            float64 `json:"time"`
}

// VideoName carries the relative video URLs the front-end consumes.
type VideoName struct {
	MP4 string `json:"mp4"`
	JPG string `json:"jpg"`
}

// GenerateFinish produces a complete FinishData block for one race.
//
// Algorithm follows finish.ts:137-225 with one architect-mandated
// deviation: positions 3..N are NOT shuffled — they come straight from
// the selected pool entry's Order. Phase 2 ships the full Order from
// production ES data, so the shuffle the TS code applied (a fallback
// for missing enriched data) is no longer needed.
func GenerateFinish(mt rng.Source, cfg config.GameTypeConfigExt, sel *videoselector.Selector) FinishData {
	pick := sel.Select(mt)

	time1 := rng.CertifiedFloatRange(mt, cfg.FinishTimeRange.Min, cfg.FinishTimeRange.Max, 2)
	rawGap := math.Pow(rng.CertifiedFloat(mt), cfg.GapExponent)
	gap := roundN(cfg.GapRange.Min+rawGap*(cfg.GapRange.Max-cfg.GapRange.Min), 2)
	time2 := roundN(time1+gap, 2)

	// Finish map: positions 1..N use pick.Order directly. Positions 1
	// and 2 carry the computed times; positions 3..N use a nil Time
	// pointer (serializes as JSON null).
	finish := make(map[string]FinishPosition, cfg.NumberCompetitor)
	t1 := time1
	t2 := time2
	for k := 1; k <= cfg.NumberCompetitor; k++ {
		runner := pick.Order[k-1]
		fp := FinishPosition{CompetitorIndex: runner, Time: nil}
		switch k {
		case 1:
			fp.Time = &t1
		case 2:
			fp.Time = &t2
		}
		finish[strconv.Itoa(k)] = fp
	}

	first := pick.Order[0]
	second := pick.Order[1]

	interval := generateIntervals(mt, cfg, first, second)
	videoName := buildVideoName(cfg, pick.VideoID)

	return FinishData{
		Finish:    finish,
		Interval:  interval,
		VideoName: videoName,
		First:     first,
		Second:    second,
		order:     append([]int(nil), pick.Order...),
	}
}

// Order returns the 1-based finish order (Order()[0] is the first-place
// runner). The returned slice is the internal slice; callers must not
// mutate it. Used by GenerateOdds to couple WIN-odds-value assignment to
// the chosen finish order.
func (f FinishData) Order() []int { return f.order }

// generateIntervals builds 1 or 2 interval checkpoints driven by
// cfg.IntervalCount. Per legacy finish.ts:171-225:
//
//   - Checkpoint 1: 50% probability the leader matches the final first;
//     second rank uniform-distinct in [1, N]. Times from
//     cfg.Interval1TimeRange, with +0.05/+0.06 offsets for rank 2.
//   - Checkpoint 2 (if IntervalCount >= 2): 75% first match, 70% second
//     match. Times from cfg.Interval2TimeRange with +0.07/+0.07.
func generateIntervals(mt rng.Source, cfg config.GameTypeConfigExt, finalFirst, finalSecond int) map[string]map[string]IntervalPosition {
	n := cfg.NumberCompetitor
	out := make(map[string]map[string]IntervalPosition, cfg.IntervalCount)

	// Checkpoint 1.
	var i1First int
	if rng.CertifiedFloat(mt) < 0.50 {
		i1First = finalFirst
	} else {
		i1First = rng.CertifiedInt(mt, 1, n)
	}
	i1Second := rng.CertifiedInt(mt, 1, n)
	for i1Second == i1First {
		i1Second = rng.CertifiedInt(mt, 1, n)
	}
	out["1"] = map[string]IntervalPosition{
		"1": {
			CompetitorIndex: i1First,
			Time:            rng.CertifiedFloatRange(mt, cfg.Interval1TimeRange.Min, cfg.Interval1TimeRange.Max, 2),
		},
		"2": {
			CompetitorIndex: i1Second,
			Time:            rng.CertifiedFloatRange(mt, cfg.Interval1TimeRange.Min+0.05, cfg.Interval1TimeRange.Max+0.06, 2),
		},
	}

	if cfg.IntervalCount < 2 {
		return out
	}

	// Checkpoint 2.
	var i2First int
	if rng.CertifiedFloat(mt) < 0.75 {
		i2First = finalFirst
	} else {
		i2First = rng.CertifiedInt(mt, 1, n)
	}
	var i2Second int
	if rng.CertifiedFloat(mt) < 0.70 {
		i2Second = finalSecond
	} else {
		i2Second = rng.CertifiedInt(mt, 1, n)
	}
	for i2Second == i2First {
		i2Second = rng.CertifiedInt(mt, 1, n)
	}
	out["2"] = map[string]IntervalPosition{
		"1": {
			CompetitorIndex: i2First,
			Time:            rng.CertifiedFloatRange(mt, cfg.Interval2TimeRange.Min, cfg.Interval2TimeRange.Max, 2),
		},
		"2": {
			CompetitorIndex: i2Second,
			Time:            rng.CertifiedFloatRange(mt, cfg.Interval2TimeRange.Min+0.07, cfg.Interval2TimeRange.Max+0.07, 2),
		},
	}
	return out
}

// buildVideoName parses the trailing integer from videoID (e.g.
// "DOG8#241" → 241), formats it as R-padded (R0241), and assembles
// the mp4/jpg URL pair under cfg.VideoPoolPath with cfg.VideoFileSuffix.
// Matches finish.ts:69-79.
func buildVideoName(cfg config.GameTypeConfigExt, videoID string) VideoName {
	// Literal-name pools (horse_classic): the pool ID IS the real video file
	// stem (the 7-digit finishing order, e.g. "1237465" → 1237465.mp4 under
	// DSVideo/horse/). Use it verbatim — no "#"/R%04d reformat.
	if cfg.VideoNameLiteral {
		base := "/.local/" + cfg.VideoPoolPath + "/" + videoID + cfg.VideoFileSuffix
		return VideoName{MP4: base + ".mp4", JPG: base + ".jpg"}
	}
	num := 1 // legacy fallback when parsing fails
	if hash := strings.Index(videoID, "#"); hash >= 0 && hash+1 < len(videoID) {
		if v, err := strconv.Atoi(videoID[hash+1:]); err == nil {
			num = v
		}
	}
	r := fmt.Sprintf("R%04d", num)
	base := "/.local/" + cfg.VideoPoolPath + "/" + r + cfg.VideoFileSuffix
	return VideoName{MP4: base + ".mp4", JPG: base + ".jpg"}
}
