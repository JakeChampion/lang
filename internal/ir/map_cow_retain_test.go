package ir

import "testing"

// The COW-seam retain (#6227) fires at BINDING sites and nowhere else.
//
// `__map_cow_inplace` hands the receiver's own handle back when the map is
// uniquely held, so a mutator result that someone BINDS shares one refcount
// with the receiver's binding and both release it — silent entry loss on
// `insert`, a SEGV on `without`. The retain closes that.
//
// The gate matters as much as the retain. In every position where the result
// is a TEMPORARY nobody binds — a chained receiver, a call argument, a
// projected `.0`, the direct self-rebind whose COW-aware overwrite already
// balances it — nothing releases the extra count, so retaining there trades a
// double-free for an unbounded leak: measured at ~1.8 kB per evaluation, a
// whole copied table each time round the loop.
//
// So this counts rc-incs per function, against the counts the same bodies
// emitted BEFORE the seam retain existed: 0, 1, 0 for the three binding
// shapes and 0, 0, 0, 1 for the four temporary ones. The retain adds exactly
// one inc to each binding shape and nothing anywhere else. The two non-zero
// baselines are not the seam and must not move — `bindtuple` retains the map
// it projects out of the destructured tuple, and `projected` retains the map
// it projects out of `.0`; both are tuple-element retains that predate this.
//
// i32 keys and values keep the counts this small: a string key or value would
// add per-column retains that have nothing to do with the seam.
func TestMapCowRetainOnlyAtBindingSites(t *testing.T) {
	src := `function bindvar(n: i32): i32 {
    var m: Map[i32, i32] = map_new(64);
    var m2 = m.insert(1, n);
    m = m2;
    return m.len();
}
function bindtuple(n: i32): i32 {
    var m: Map[i32, i32] = map_new(64);
    m = m.insert(1, n);
    var (m2, ok) = m.without(1);
    m = m2;
    return m.len();
}
function bindclear(n: i32): i32 {
    var m: Map[i32, i32] = map_new(64);
    m = m.insert(1, n);
    var m2 = m.cleared();
    m = m2;
    return m.len();
}
function direct(n: i32): i32 {
    var m: Map[i32, i32] = map_new(64);
    m = m.insert(1, n);
    return m.len();
}
function chained(n: i32): i32 {
    var m: Map[i32, i32] = map_new(64);
    m = m.insert(1, n).insert(2, n);
    return m.len();
}
function sizeof(m: Map[i32, i32]): i32 { return m.len(); }
function argpos(n: i32): i32 {
    var m: Map[i32, i32] = map_new(64);
    return sizeof(m.insert(1, n));
}
function projected(n: i32): i32 {
    var m: Map[i32, i32] = map_new(64);
    m = m.insert(1, n);
    m = m.without(1).0;
    return m.len();
}
function main(): i32 { return 0; }`

	// name -> rc-incs owed. The binding shapes carry the seam retain; the
	// temporary ones must stay at their pre-#6227 baseline.
	retaining := map[string]int{"bindvar": 1, "bindtuple": 2, "bindclear": 1}
	temporary := map[string]int{"direct": 0, "chained": 0, "argpos": 0, "projected": 1}

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		for fn, want := range retaining {
			if n := countRcIncs(prog, fn); n != want {
				t.Errorf("ptrW=%d: %s emitted %d rc-incs, want %d — one of them the COW-seam retain, without which the binding and the receiver share one refcount and both release it (#6227)", ptrW, fn, n, want)
			}
		}
		for fn, want := range temporary {
			if n := countRcIncs(prog, fn); n != want {
				t.Errorf("ptrW=%d: %s emitted %d rc-incs, want %d — the mutator result here is a temporary nobody binds, and a retain on it leaks a whole table per evaluation", ptrW, fn, n, want)
			}
		}
	}
}
