package rng

// Source is the abstraction every consumer of certified randomness depends
// on. Production wires an HMAC-DRBG (SP 800-90A) seeded from the OS CSPRNG;
// tests and the gli_lab build may inject an MT19937 with a fixed seed for
// byte-exact replay.
//
// Implementations are NOT required to be thread-safe; the race-generator
// scheduler consumes a single Source serially from one goroutine. Wrap with
// a mutex (or use one Source per worker) before introducing concurrency.
type Source interface {
	// NextUint32 emits a uniform uint32 in [0, 2^32-1].
	NextUint32() uint32
	// GenerationCount returns how many uint32s have been emitted since
	// instantiation. Used by the audit log (mtSeqAfter) to fingerprint
	// per-round stream consumption.
	GenerationCount() uint64
}

// Reseeder is implemented by sources that support explicit reseeding
// (HMACDRBG). The orchestrator reseeds at every round boundary — background
// cycling with real prediction resistance (GLI-19 §3.2.6). Sources without
// reseed support (MT19937, tests) simply skip this step.
type Reseeder interface {
	Reseed(additional []byte) error
	ReseedCount() uint64
}

// Advance discards n values from the source (GLI-19 background cycling).
func Advance(src Source, n int) {
	for i := 0; i < n; i++ {
		_ = src.NextUint32()
	}
}
