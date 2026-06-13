package generators

// NameCooldown is a FIFO ring tracking the most recent N competitor names
// emitted across rounds. Membership is O(1) via the set; eviction is O(1)
// by popping the oldest entry off the queue when capacity is exceeded.
// Capacity equals NumberCompetitor*10 — matching the legacy TS generator's
// 10-slot anti-repetition window (see virteon-platform GeneratorDataSource.ts:178-217).
//
// It lives in generators (not in the scheduler binary) because the name
// cooldown is part of the per-round generation semantics: the GLI Final
// Outcome Collection harness (cmd/rngextract) must replicate the EXACT
// production draw sequence, exclude-set included, or its evidence would not
// represent the certified pipeline.
type NameCooldown struct {
	set      map[string]struct{}
	queue    []string
	capacity int
}

// NewNameCooldown returns an empty cooldown ring sized for capacity entries.
// capacity must be > 0; callers derive it from cfg.NumberCompetitor*10.
func NewNameCooldown(capacity int) *NameCooldown {
	if capacity <= 0 {
		capacity = 1
	}
	return &NameCooldown{
		set:      make(map[string]struct{}, capacity),
		queue:    make([]string, 0, capacity),
		capacity: capacity,
	}
}

// Add pushes name onto the FIFO. If the ring is at capacity, evicts the
// oldest entry first. No-op if name is already present (preserves its
// original insertion order — the legacy "the same name stays cool for 10
// rounds from its first sighting" semantics).
func (c *NameCooldown) Add(name string) {
	if _, exists := c.set[name]; exists {
		return
	}
	if len(c.queue) >= c.capacity {
		oldest := c.queue[0]
		c.queue = c.queue[1:]
		delete(c.set, oldest)
	}
	c.queue = append(c.queue, name)
	c.set[name] = struct{}{}
}

// Excludes returns the set-as-map[string]bool expected by
// GenerateCompetitors. Allocates fresh each call so the generator cannot
// mutate the cooldown's internal state.
func (c *NameCooldown) Excludes() map[string]bool {
	out := make(map[string]bool, len(c.set))
	for n := range c.set {
		out[n] = true
	}
	return out
}

// Len returns the current number of names in the cooldown.
func (c *NameCooldown) Len() int { return len(c.queue) }
