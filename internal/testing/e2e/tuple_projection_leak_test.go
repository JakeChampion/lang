package e2e

import (
	"testing"
)

// --- Projecting a pointer field out of a match binding, and its reclaim ------
//
// `Some(t) => cur = t.1`, where `t` is a (value, array) pair, is the shape
// `core/iter`'s filter, map and enumerate are written on. The projected
// array is owned like any other value, so the overwrite releases the
// previous one and nothing is left at exit. The neighbours each differ by one
// thing (the binding unused, only the scalar projected, no pointer field at
// all) and pin the boundary.
//
// Every array embeds the loop counter: a constant literal is a static
// aggregate the tracer never sees allocated.
//
// docs/rc-log/2026-08-30-match-binding-rebind-overretain.md has the native
// investigation this test came from.

const tupleProjLeakSrc = `
function step(n: i32): Option[(i32, u8[])] {
    if (n < 3) { return Some((n + 1, [n as u8, 2, 3])); }
    return None;
}
function main(): i32 {
    let cur: u8[] = [0];
    let i: i32 = 0;
    let go: boolean = true;
    while (go) {
        match (step(i)) {
            Some(t) => { i = t.0; cur = t.1; },
            None => { go = false; },
        }
    }
    return cur.len();
}
`

// The three clean neighbours. Each differs from the case above by one
// thing, so together they say what the trigger is rather than just that
// there is one.
const (
	tupleProjBindingUnusedSrc = `
function step(n: i32): Option[(i32, u8[])] {
    if (n < 3) { return Some((n + 1, [n as u8, 2, 3])); }
    return None;
}
function main(): i32 {
    let i: i32 = 0; let go: boolean = true;
    while (go) { match (step(i)) { Some(t) => { i = i + 1; }, None => { go = false; }, } }
    return i;
}
`
	tupleProjScalarOnlySrc = `
function step(n: i32): Option[(i32, u8[])] {
    if (n < 3) { return Some((n + 1, [n as u8, 2, 3])); }
    return None;
}
function main(): i32 {
    let i: i32 = 0; let go: boolean = true;
    while (go) { match (step(i)) { Some(t) => { i = t.0; }, None => { go = false; }, } }
    return i;
}
`
	tupleProjNoPointersSrc = `
function step(n: i32): Option[(i32, i32)] {
    if (n < 3) { return Some((n + 1, 7)); }
    return None;
}
function main(): i32 {
    let i: i32 = 0; let go: boolean = true;
    while (go) { match (step(i)) { Some(t) => { i = t.0; }, None => { go = false; }, } }
    return i;
}
`
)

func TestTupleProjectionFromMatchBindingLeaksX86_64(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs; not a -short test")
	}
	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{"pointer field projected", tupleProjLeakSrc, 0},
		// The boundary.
		{"binding unused", tupleProjBindingUnusedSrc, 0},
		{"scalar field only", tupleProjScalarOnlySrc, 0},
		{"tuple has no pointers", tupleProjNoPointersSrc, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := unpairedAllocs(t, tc.src)
			if got != tc.want {
				t.Errorf("%d unpaired allocation(s), want %d — a projection out of a "+
					"match binding is owned like any other value, so its overwrite "+
					"releases the previous one",
					got, tc.want)
			}
		})
	}
}
