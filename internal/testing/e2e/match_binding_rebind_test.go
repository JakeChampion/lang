package e2e

import (
	"testing"
)

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
