package e2e

import (
	"testing"
)

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
