package ir

import "testing"

// The COW-seam retain (#6227) fires exactly where something RELEASES the
// handle it hands out, and nowhere else.
//
// `__map_cow_inplace` hands the receiver's own handle back when the map is
// uniquely held, so a mutator result that someone binds shares one refcount
// with the receiver's binding and both release it — silent entry loss on
// `insert`, a SEGV on `without`. The retain closes that.
//
// The gate matters as much as the retain: where the result is a temporary
// nothing ever drops — a chained receiver, a call argument — the extra count
// is returned by no one, so retaining there trades a double-free for an
// unbounded leak, ~1.8 kB per evaluation, a whole copied table each time round
// the loop.
//
// A PROJECTED delete tuple looks like that second class and is not. Its box is
// a temporary, but the field read deep-drops it (freshOwnedFieldContainer
// admits a seam-retained delete tuple), and that drop releases the map
// element — so the retain is what makes the drop safe, exactly as at a binding
// site. Retain and drop are a pair here too: `projected` carried neither and
// leaked the whole table plus the undropped box, 128000 / 144000 / 104000 B
// over the corpus fixture (#8434).
//
// So this counts rc-incs per function. `bindtuple` and `projected` sit at 2 —
// one seam retain plus one tuple-element retain that predates it — the other
// two binding shapes at 1, and the three genuine temporaries at 0.
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
function argproj(n: i32): i32 {
    var m: Map[i32, i32] = map_new(64);
    m = m.insert(1, n);
    return sizeof(m.without(1).0);
}
function main(): i32 { return 0; }`

	// name -> rc-incs owed. The binding shapes carry the seam retain; the
	// temporary ones must stay at their pre-#6227 baseline.
	retaining := map[string]int{"bindvar": 1, "bindtuple": 2, "bindclear": 1, "projected": 2, "argproj": 2}
	temporary := map[string]int{"direct": 0, "chained": 0, "argpos": 0}

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		for fn, want := range retaining {
			if n := countRcIncs(prog, fn); n != want {
				t.Errorf("ptrW=%d: %s emitted %d rc-incs, want %d — one of them the COW-seam retain, without which the binding and the receiver share one refcount and both release it (#6227)", ptrW, fn, n, want)
			}
		}
		for fn, want := range temporary {
			if n := countRcIncs(prog, fn); n != want {
				t.Errorf("ptrW=%d: %s emitted %d rc-incs, want %d — the mutator result here is a temporary nothing drops, and a retain on it leaks a whole table per evaluation", ptrW, fn, n, want)
			}
		}
	}
}
