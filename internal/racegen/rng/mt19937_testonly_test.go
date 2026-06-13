package rng

import "errors"

// Test-only MT19937 helpers. These lived in mt19937.go as public API; they
// were removed from the package surface because exposing RNG state snapshots
// is the R4 hazard this branch eliminates (see docs/AUDITORIA-RNG-GLI19.md).
// Methods declared in _test.go files never ship in any binary.

// NextFloat emite un float64 uniforme en [0, 1) con 32 bits de resolución
// (la semántica histórica del MT — solo para benchmarks/tests de paridad).
func (mt *MT19937) NextFloat() float64 {
	return float64(mt.NextUint32()) / 4294967296.0
}

// State es una copia del estado interno para snapshot/reproducibilidad.
type State struct {
	S   [mtN]uint32 `json:"s"`
	Idx int         `json:"idx"`
	Gen uint64      `json:"gen"`
}

// State devuelve una copia del estado interno (independiente del MT19937).
func (mt *MT19937) State() State {
	return State{S: mt.state, Idx: mt.index, Gen: mt.gen}
}

var errStateInvalid = errors.New("estado MT19937 inválido")

// RestoreState pone el RNG en un estado previo.
func (mt *MT19937) RestoreState(s State) error {
	if s.Idx < 0 || s.Idx > mtN {
		return errStateInvalid
	}
	mt.state = s.S
	mt.index = s.Idx
	mt.gen = s.Gen
	return nil
}
