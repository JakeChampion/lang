package e2e

import (
	"testing"
)

// --- Assigning a match binding out of its arm ------------------------------
//
// `cur = v`, where `v` is a match-destructured binding, leaves `cur` the only
// owner: the assignment retains what it reads, and the arm gives back the
// reference the callee handed over. Every eager adapter in `core/iter` is
// written on this loop, so a reference too many costs one value per element.
// A value held once more than its owners release is still allocated at exit,
// which is what the census counts.
//
// Every array embeds the loop counter: a constant literal is a static
// aggregate the tracer never sees allocated.
//
// docs/rc-log/2026-08-30-match-binding-rebind-overretain.md has the native
// investigation; docs/rc-log/2026-09-21-a-counted-escape-is-not-an-escape.md
// has the repair.

const matchBindingRebindSrc = `
function pick(n: i32): Option[u8[]] {
    if (n < 3) { return Some([n as u8, 2, 3]); }
    return None;
}
function main(): i32 {
    let cur: u8[] = [0];
    let i: i32 = 0;
    let go: boolean = true;
    while (go) {
        match (pick(i)) {
            Some(v) => { cur = v; i = i + 1; },
            None => { go = false; },
        }
    }
    return cur.len();
}
`

// The same loop with a fresh literal rather than the binding — the control,
// and the shape the case above now matches.
const freshRebindSrc = `
function main(): i32 {
    let cur: u8[] = [0];
    let i: i32 = 0;
    while (i < 3) { cur = [i as u8, 2, 3]; i = i + 1; }
    return cur.len();
}
`

func TestMatchBindingRebindOwnsOnceX86_64(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs; not a -short test")
	}
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"match binding", matchBindingRebindSrc},
		// The control: a fresh value assigned in the same loop.
		{"fresh literal", freshRebindSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := unpairedAllocs(t, tc.src); got != 0 {
				t.Errorf("%d unpaired allocation(s), want 0: cur holds its value once "+
					"more than it releases, which strands one value per iteration "+
					"through every core/iter adapter", got)
			}
		})
	}
}
