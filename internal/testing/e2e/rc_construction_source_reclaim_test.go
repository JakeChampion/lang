package e2e

import (
	"strconv"
	"testing"
)

func tupleDiscardBareIdentSrc(rounds int) string {
	return `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    (xs, [i + 2, i + 3]);
    return xs[0] + xs[1];
}
` + churnMain(rounds, rounds*rounds)
}

func structDiscardBareIdentSrc(rounds int) string {
	return `struct Holder { a: i32[], b: i32[] }
function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    Holder { a: xs, b: [i + 2, i + 3] };
    return xs[0] + xs[1];
}
` + churnMain(rounds, rounds*rounds)
}

func tupleEscapingBareIdentSrc(rounds int) string {
	return `function mk(i: i32): (i32[], i32[]) {
    let xs: i32[] = [i, i + 1];
    let t: (i32[], i32[]) = (xs, [i + 2, i + 3]);
    let guard: i32 = xs[1];
    if (guard < 0) { return (xs, xs); }
    return t;
}
function round(i: i32): i32 {
    let t: (i32[], i32[]) = mk(i);
    return t.0[0] + t.0[1] + t.1[0] + t.1[1];
}
` + churnMain(rounds, 2*rounds*rounds+4*rounds)
}

func churnMain(rounds, want int) string {
	return `function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
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
