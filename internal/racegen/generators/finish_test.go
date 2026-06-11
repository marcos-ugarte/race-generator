package generators

import (
	"encoding/json"
	"testing"

	"vg-racegen/internal/racegen/data"
	"vg-racegen/internal/racegen/videoselector"
)

// poolSelector builds a Selector from a single-entry pool, so tests can
// pin the Select result deterministically without an interface.
func poolSelector(t *testing.T, gameType string, videoID string, order []int) *videoselector.Selector {
	t.Helper()
	cfg := mustConfig(t, gameType)
	pool := &data.Pool{
		GameType: gameType,
		NumComp:  cfg.NumberCompetitor,
		Entries: []data.VideoFinish{
			{ID: videoID, Order: order},
		},
	}
	sel, err := videoselector.New(pool, cfg)
	if err != nil {
		t.Fatalf("videoselector.New: %v", err)
	}
	return sel
}

// realSelector wraps mustSelector but lives here to avoid dragging the
// videoselector test file into the generators package.
func realSelector(t *testing.T, gameType string) *videoselector.Selector {
	t.Helper()
	cfg := mustConfig(t, gameType)
	pool := data.VideoPool(gameType)
	if pool == nil {
		t.Fatalf("nil pool for %s", gameType)
	}
	sel, err := videoselector.New(pool, cfg)
	if err != nil {
		t.Fatalf("videoselector.New: %v", err)
	}
	return sel
}

func TestFinishShape_dog8(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	sel := realSelector(t, "dog8")
	got := GenerateFinish(mustMT(t), cfg, sel)
	if len(got.Finish) != 8 {
		t.Fatalf("len(Finish)=%d, want 8", len(got.Finish))
	}
	if len(got.Interval) < 1 || len(got.Interval) > 2 {
		t.Fatalf("len(Interval)=%d, want 1 or 2", len(got.Interval))
	}
	// Positions 1 and 2: non-nil Time. Positions 3..N: nil Time.
	for k := 1; k <= 8; k++ {
		key := itoa(k)
		fp, ok := got.Finish[key]
		if !ok {
			t.Fatalf("missing finish key %s", key)
		}
		switch k {
		case 1, 2:
			if fp.Time == nil {
				t.Errorf("pos %d: Time is nil", k)
			}
		default:
			if fp.Time != nil {
				t.Errorf("pos %d: Time=%v, want nil", k, *fp.Time)
			}
		}
	}
}

func TestFinishShape_dog6(t *testing.T) {
	cfg := mustConfig(t, "dog6")
	sel := realSelector(t, "dog6")
	got := GenerateFinish(mustMT(t), cfg, sel)
	if len(got.Finish) != 6 {
		t.Fatalf("len(Finish)=%d, want 6", len(got.Finish))
	}
}

func TestFinishFirstSecondMatchSelector(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	order := []int{3, 7, 1, 5, 4, 8, 2, 6}
	sel := poolSelector(t, "dog8", "DOG8#241", order)
	got := GenerateFinish(mustMT(t), cfg, sel)
	if got.First != order[0] {
		t.Errorf("First=%d, want %d", got.First, order[0])
	}
	if got.Second != order[1] {
		t.Errorf("Second=%d, want %d", got.Second, order[1])
	}
}

func TestFinishCompetitorsUnique(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	sel := realSelector(t, "dog8")
	got := GenerateFinish(mustMT(t), cfg, sel)
	seen := make(map[int]struct{}, cfg.NumberCompetitor)
	for k := 1; k <= cfg.NumberCompetitor; k++ {
		fp := got.Finish[itoa(k)]
		if _, dup := seen[fp.CompetitorIndex]; dup {
			t.Errorf("position %d: duplicate runner %d", k, fp.CompetitorIndex)
		}
		seen[fp.CompetitorIndex] = struct{}{}
		if fp.CompetitorIndex < 1 || fp.CompetitorIndex > cfg.NumberCompetitor {
			t.Errorf("position %d: runner %d outside [1,%d]", k, fp.CompetitorIndex, cfg.NumberCompetitor)
		}
	}
	if len(seen) != cfg.NumberCompetitor {
		t.Errorf("got %d unique runners, want %d", len(seen), cfg.NumberCompetitor)
	}
}

// CRITICAL: with a fixed Order, every finish position must mirror that
// order — no shuffle on positions 3..N.
func TestFinishOrderMatchesPoolEntry(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	order := []int{3, 7, 1, 5, 4, 8, 2, 6}
	sel := poolSelector(t, "dog8", "DOG8#777", order)
	got := GenerateFinish(mustMT(t), cfg, sel)
	for k := 1; k <= cfg.NumberCompetitor; k++ {
		want := order[k-1]
		fp := got.Finish[itoa(k)]
		if fp.CompetitorIndex != want {
			t.Errorf("finish[%d].CompetitorIndex=%d, want %d", k, fp.CompetitorIndex, want)
		}
	}
}

func TestFinishVideoNamePath_dog8(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	order := []int{1, 2, 3, 4, 5, 6, 7, 8}
	sel := poolSelector(t, "dog8", "DOG8#241", order)
	got := GenerateFinish(mustMT(t), cfg, sel)
	const wantMP4 = "/.local/dog8/R0241_h.mp4"
	const wantJPG = "/.local/dog8/R0241_h.jpg"
	if got.VideoName.MP4 != wantMP4 {
		t.Errorf("VideoName.MP4=%q, want %q", got.VideoName.MP4, wantMP4)
	}
	if got.VideoName.JPG != wantJPG {
		t.Errorf("VideoName.JPG=%q, want %q", got.VideoName.JPG, wantJPG)
	}
}

// horse_classic uses literal video names: the pool ID IS the real 7-digit
// finish-order file stem, served verbatim (no "R%04d"). The mp4 served must be
// the file whose name encodes the finish — the GLI binding.
func TestFinishVideoNamePath_horseClassic(t *testing.T) {
	cfg := mustConfig(t, "horse_classic")
	order := []int{1, 2, 3, 7, 4, 6, 5} // == digits of "1237465"
	sel := poolSelector(t, "horse_classic", "1237465", order)
	got := GenerateFinish(mustMT(t), cfg, sel)
	const wantMP4 = "/.local/horse_classic/1237465.mp4"
	const wantJPG = "/.local/horse_classic/1237465.jpg"
	if got.VideoName.MP4 != wantMP4 {
		t.Errorf("VideoName.MP4=%q, want %q", got.VideoName.MP4, wantMP4)
	}
	if got.VideoName.JPG != wantJPG {
		t.Errorf("VideoName.JPG=%q, want %q", got.VideoName.JPG, wantJPG)
	}
}

func TestFinishIntervalCount(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	sel := realSelector(t, "dog8")
	got := GenerateFinish(mustMT(t), cfg, sel)
	if len(got.Interval) != cfg.IntervalCount {
		t.Fatalf("len(Interval)=%d, want IntervalCount=%d", len(got.Interval), cfg.IntervalCount)
	}
}

func TestFinishDeterministic(t *testing.T) {
	cfg := mustConfig(t, "dog8")
	selA := realSelector(t, "dog8")
	selB := realSelector(t, "dog8")
	a := GenerateFinish(mustMT(t), cfg, selA)
	b := GenerateFinish(mustMT(t), cfg, selB)
	ja, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal a: %v", err)
	}
	jb, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal b: %v", err)
	}
	if string(ja) != string(jb) {
		t.Fatalf("non-deterministic:\n a=%s\n b=%s", ja, jb)
	}
}

// itoa is a tiny package-local helper so we don't import strconv into
// every test name lookup.
func itoa(i int) string {
	switch i {
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	case 4:
		return "4"
	case 5:
		return "5"
	case 6:
		return "6"
	case 7:
		return "7"
	case 8:
		return "8"
	}
	// fallback for any future game type with N > 8
	buf := make([]byte, 0, 4)
	if i < 0 {
		buf = append(buf, '-')
		i = -i
	}
	var digits [16]byte
	n := 0
	if i == 0 {
		digits[n] = '0'
		n++
	}
	for i > 0 {
		digits[n] = byte('0' + i%10)
		i /= 10
		n++
	}
	for k := n - 1; k >= 0; k-- {
		buf = append(buf, digits[k])
	}
	return string(buf)
}
