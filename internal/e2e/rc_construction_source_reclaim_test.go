package e2e

import (
	"strconv"
	"testing"
)

// A local consumed by an INC-ing construction sink (tuple / struct literal)
// keeps a reference of its own, so it must still be reclaimed at scope exit
// (#7345). The container's deep drop releases the construction dup; the
// local's sweep releases the local's reference. Suppressing the second one
// stranded the buffer whenever the container died FIRST — a discarded literal
// statement, where the drop runs mid-body and the sweep then only decremented
// a flat rc that never reached a free.
//
// Every probe is loop-resident (the allocation lives inside `round`, so it
// scales with the round count) and is measured at three round counts: a leak
// here is per-round and unbounded, which one count cannot distinguish from a
// fixed startup cost. The exit code folds in `__rc_underflow_count()`, because
// the census is structurally blind to an over-release — allocs == frees at
// live_bytes 0 is exactly what a double free into the freelist looks like.

// tupleDiscardBareIdentSrc: the reported shape. `xs` is retained by the tuple
// box, the tuple is discarded as a statement, and `xs` is read afterwards (so
// no move applies).
func tupleDiscardBareIdentSrc(rounds int) string {
	return `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    (xs, [i + 2, i + 3]);
    return xs[0] + xs[1];
}
` + churnMain(rounds, rounds*rounds)
}

// structDiscardBareIdentSrc: the struct-literal sibling, which shared the
// defect — StructLit retains a bare-ident field under the same predicate.
func structDiscardBareIdentSrc(rounds int) string {
	return `struct Holder { a: i32[], b: i32[] }
function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    Holder { a: xs, b: [i + 2, i + 3] };
    return xs[0] + xs[1];
}
` + churnMain(rounds, rounds*rounds)
}

// tupleEscapingBareIdentSrc is the other direction: the tuple OUTLIVES the
// function that built it, so the source local's sweep must only decrement.
// Reclaiming its buffer there would hand the caller a freed element — the
// value check and the underflow counter both catch that.
func tupleEscapingBareIdentSrc(rounds int) string {
	return `function mk(i: i32): (i32[], i32[]) {
    var xs: i32[] = [i, i + 1];
    var t: (i32[], i32[]) = (xs, [i + 2, i + 3]);
    var guard: i32 = xs[1];
    if (guard < 0) { return (xs, xs); }
    return t;
}
function round(i: i32): i32 {
    var t: (i32[], i32[]) = mk(i);
    return t.0[0] + t.0[1] + t.1[0] + t.1[1];
}
` + churnMain(rounds, 2*rounds*rounds+4*rounds)
}

// churnMain drives `round` for the given number of rounds and returns 0 only
// when the accumulated value matches and no rc over-release was counted.
func churnMain(rounds, want int) string {
	return `function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < ` + strconv.Itoa(rounds) + `) { acc = acc + round(r); r = r + 1; }
    if (acc != ` + strconv.Itoa(want) + `) { return 1; }
    if (__rc_underflow_count() != 0) { return 2; }
    return 0;
}`
}

func TestX86_64ConstructionSourceReclaim(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  func(int) string
	}{
		{"tuple_literal_discarded", tupleDiscardBareIdentSrc},
		{"struct_literal_discarded", structDiscardBareIdentSrc},
		{"tuple_escapes_caller", tupleEscapingBareIdentSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, rounds := range []int{100, 200, 400} {
				name := tc.name + "/" + strconv.Itoa(rounds)
				allocs, frees, live := leakCounts(t, name, tc.src(rounds), 0)
				if live != 0 || allocs != frees {
					t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census — "+
						"a source retained by a construction sink must still be reclaimed at scope exit",
						name, allocs, frees, live)
				}
			}
		})
	}
}
