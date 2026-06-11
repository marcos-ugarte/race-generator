package data

import (
	"strings"
	"testing"

	"vg-racegen/internal/racegen/rng"
)

func TestDogNamesNonEmpty(t *testing.T) {
	names := DogNames()
	if len(names) < 1000 {
		t.Fatalf("DogNames: got %d entries, want >= 1000", len(names))
	}
	seen := make(map[string]struct{}, len(names))
	for i, n := range names {
		if strings.TrimSpace(n) == "" {
			t.Fatalf("DogNames: entry %d is empty", i)
		}
		if _, dup := seen[n]; dup {
			t.Fatalf("DogNames: duplicate entry %q (index %d)", n, i)
		}
		seen[n] = struct{}{}
	}
}

func TestVideoPoolDog8(t *testing.T) {
	p := VideoPool("dog8")
	if p == nil {
		t.Fatal("VideoPool(dog8) is nil")
	}
	if p.Len() != 411 {
		t.Fatalf("dog8 pool size: got %d, want 411", p.Len())
	}
	if p.NumComp != 8 {
		t.Fatalf("dog8 NumComp: got %d, want 8", p.NumComp)
	}
	for i := 0; i < p.Len(); i++ {
		e := p.At(i)
		if len(e.Order) != 8 {
			t.Fatalf("entry %s: len(Order)=%d, want 8", e.ID, len(e.Order))
		}
		seen := make(map[int]struct{}, 8)
		for pos, runner := range e.Order {
			if runner < 1 || runner > 8 {
				t.Fatalf("entry %s pos %d: runner %d out of [1,8]", e.ID, pos, runner)
			}
			if _, dup := seen[runner]; dup {
				t.Fatalf("entry %s: duplicate runner %d in Order", e.ID, runner)
			}
			seen[runner] = struct{}{}
		}
	}
}

func TestVideoPoolDog6(t *testing.T) {
	p := VideoPool("dog6")
	if p == nil {
		t.Fatal("VideoPool(dog6) is nil")
	}
	if p.Len() != 979 {
		t.Fatalf("dog6 pool size: got %d, want 979", p.Len())
	}
	if p.NumComp != 6 {
		t.Fatalf("dog6 NumComp: got %d, want 6", p.NumComp)
	}
	for i := 0; i < p.Len(); i++ {
		e := p.At(i)
		if len(e.Order) != 6 {
			t.Fatalf("entry %s: len(Order)=%d, want 6", e.ID, len(e.Order))
		}
		seen := make(map[int]struct{}, 6)
		for pos, runner := range e.Order {
			if runner < 1 || runner > 6 {
				t.Fatalf("entry %s pos %d: runner %d out of [1,6]", e.ID, pos, runner)
			}
			if _, dup := seen[runner]; dup {
				t.Fatalf("entry %s: duplicate runner %d in Order", e.ID, runner)
			}
			seen[runner] = struct{}{}
		}
	}
}

func TestVideoPoolHorseClassic(t *testing.T) {
	p := VideoPool("horse_classic")
	if p == nil {
		t.Fatal("VideoPool(horse_classic) is nil")
	}
	if p.Len() != 338 {
		t.Fatalf("horse_classic pool size: got %d, want 338", p.Len())
	}
	if p.NumComp != 7 {
		t.Fatalf("horse_classic NumComp: got %d, want 7", p.NumComp)
	}
	for i := 0; i < p.Len(); i++ {
		e := p.At(i)
		// The ID IS the real video stem: a 7-digit permutation of 1..7 whose
		// digit j equals the runner that finished in position j+1 (GLI binding:
		// file shown == finish). Verify the ID decodes to Order.
		if len(e.ID) != 7 {
			t.Fatalf("entry %s: ID len=%d, want 7-digit video stem", e.ID, len(e.ID))
		}
		for j := 0; j < 7; j++ {
			if int(e.ID[j]-'0') != e.Order[j] {
				t.Fatalf("entry %s: ID digit %d=%c but Order[%d]=%d (video must encode finish)", e.ID, j, e.ID[j], j, e.Order[j])
			}
		}
		if len(e.Order) != 7 {
			t.Fatalf("entry %s: len(Order)=%d, want 7", e.ID, len(e.Order))
		}
		seen := make(map[int]struct{}, 7)
		for pos, runner := range e.Order {
			if runner < 1 || runner > 7 {
				t.Fatalf("entry %s pos %d: runner %d out of [1,7]", e.ID, pos, runner)
			}
			if _, dup := seen[runner]; dup {
				t.Fatalf("entry %s: duplicate runner %d in Order", e.ID, runner)
			}
			seen[runner] = struct{}{}
		}
	}
}

func TestVideoPoolUnknown(t *testing.T) {
	if p := VideoPool("horse7"); p != nil {
		t.Fatalf("VideoPool(horse7): got %v, want nil", p)
	}
	if p := VideoPool(""); p != nil {
		t.Fatalf("VideoPool(\"\"): got %v, want nil", p)
	}
}

func TestPickNameDeterministic(t *testing.T) {
	const seed = uint32(0xdeadbeef)
	mt1 := rng.NewMT19937WithUint32Seed(seed)
	mt2 := rng.NewMT19937WithUint32Seed(seed)
	for i := 0; i < 100; i++ {
		a := PickName(mt1)
		b := PickName(mt2)
		if a != b {
			t.Fatalf("PickName diverged at draw %d: %q vs %q", i, a, b)
		}
	}
}

func TestPickVideoDeterministic(t *testing.T) {
	const seed = uint32(0xdeadbeef)
	for _, gt := range []string{"dog8", "dog6", "horse_classic"} {
		mt1 := rng.NewMT19937WithUint32Seed(seed)
		mt2 := rng.NewMT19937WithUint32Seed(seed)
		for i := 0; i < 100; i++ {
			a, okA := PickVideo(mt1, gt)
			b, okB := PickVideo(mt2, gt)
			if okA != okB {
				t.Fatalf("%s PickVideo ok diverged at draw %d: %v vs %v", gt, i, okA, okB)
			}
			if !okA {
				t.Fatalf("%s PickVideo returned !ok at draw %d", gt, i)
			}
			if a.ID != b.ID {
				t.Fatalf("%s PickVideo diverged at draw %d: %q vs %q", gt, i, a.ID, b.ID)
			}
		}
	}
}

func TestPoolEntriesSorted(t *testing.T) {
	for _, gt := range []string{"dog8", "dog6", "horse_classic"} {
		p := VideoPool(gt)
		if p == nil {
			t.Fatalf("VideoPool(%s) is nil", gt)
		}
		for i := 0; i+1 < p.Len(); i++ {
			if p.Entries[i].ID >= p.Entries[i+1].ID {
				t.Fatalf("%s: entries not sorted at i=%d: %q >= %q",
					gt, i, p.Entries[i].ID, p.Entries[i+1].ID)
			}
		}
	}
}

func TestPoolImmutabilityHint(t *testing.T) {
	// Pointer identity across calls: VideoPool returns the cached *Pool.
	p1 := VideoPool("dog8")
	p2 := VideoPool("dog8")
	if p1 != p2 {
		t.Fatal("VideoPool(dog8) returned different pointers across calls")
	}
	p3 := VideoPool("dog6")
	p4 := VideoPool("dog6")
	if p3 != p4 {
		t.Fatal("VideoPool(dog6) returned different pointers across calls")
	}
	p5 := VideoPool("horse_classic")
	p6 := VideoPool("horse_classic")
	if p5 != p6 {
		t.Fatal("VideoPool(horse_classic) returned different pointers across calls")
	}
}
