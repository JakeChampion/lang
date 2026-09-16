package ssa

// Value IDs are minted from one per-function counter, so they are dense: a
// table covering every value in a function is a slice indexed by ID, where a
// map costs a hash and a probe per access and an allocation per growth step.
// The passes build such a table on every run over every function, which is
// where the compiler spent most of its map time.
//
// A table is sized from the function it was built for and then read by ID, so
// every read bounds-checks: a value minted after the table was built is simply
// absent, exactly as it was absent from the map.

// idTable is a dense table of T keyed by Value.ID.
type idTable[T any] []T

// newIDTable returns a table covering every value ID in f.
func newIDTable[T any](f *Func) idTable[T] {
	if f == nil {
		return nil
	}
	return make(idTable[T], maxValueID(f, nil)+1)
}

// get returns the entry for v, or the zero value when v is invalid or was
// minted after the table was built.
func (t idTable[T]) get(v Value) T {
	if !v.IsValid() || int(v.ID) >= len(t) {
		var zero T
		return zero
	}
	return t[v.ID]
}

// set records x for v. A value outside the table is dropped, which matches
// get: the table describes the function it was built for.
func (t idTable[T]) set(v Value, x T) {
	if !v.IsValid() || int(v.ID) >= len(t) {
		return
	}
	t[v.ID] = x
}
