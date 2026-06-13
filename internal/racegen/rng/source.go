package rng

import "fmt"

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
// cycling with real prediction resistance (GLI-19 §3.2.6).
type Reseeder interface {
	Reseed(additional []byte) error
	ReseedCount() uint64
}

// CertifiedStream is what production wiring MUST hold: a Source that also
// supports round-boundary reseeding. Construction sites return this type so
// the reseed capability is a compile-time guarantee, not a silently-optional
// runtime type assert. Tests may still drive the round pipeline with a plain
// Source (MT19937) by passing a nil Reseeder explicitly.
type CertifiedStream interface {
	Source
	Reseeder
}

// EntropyError is the panic value raised when the entropy source fails
// mid-generation. It exists so recovery layers (the scheduler's panic
// isolation) can recognize an entropy failure and FAIL CLOSED — GLI-19 R11
// requires the game to stop, never to keep running without its RNG.
type EntropyError struct{ Err error }

func (e EntropyError) Error() string { return "rng: entropy failure: " + e.Err.Error() }
func (e EntropyError) Unwrap() error { return e.Err }

// BetweenRounds performs the certified round transition shared by the
// production scheduler and the GLI extraction harness — both MUST go through
// this exact sequence so the harness provably exercises "the same RNG and
// methods" (GLI Composite Submission Requirements §2.2):
//
//  1. Background cycling: discard 1-100 values, count drawn from the
//     certified stream itself (GLI-19 §3.2.6; deterministic in lab builds).
//  2. Explicit DRBG reseed (when rs is non-nil) with NO additional_input:
//     freshness comes from the EntropySource; feeding round identifiers
//     (which embed wall-clock data) into the state would make the lab
//     stream a function of start time instead of the seed alone.
//
// roundID is recorded as metadata only — it never enters the stream.
func BetweenRounds(src Source, rs Reseeder, roundID string) (*StateModification, error) {
	sm, err := ModifyStateBetweenGames(src, roundID)
	if err != nil {
		return nil, err
	}
	if rs != nil {
		if err := rs.Reseed(nil); err != nil {
			return nil, fmt.Errorf("round reseed: %w", err)
		}
	}
	return sm, nil
}

// Advance discards n values from the source (GLI-19 background cycling).
func Advance(src Source, n int) {
	for i := 0; i < n; i++ {
		_ = src.NextUint32()
	}
}
