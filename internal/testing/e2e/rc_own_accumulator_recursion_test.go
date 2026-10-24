package e2e

import (
	"strconv"
	"testing"
)

const ownAccRecursionPrelude = `enum N { L(i32), B(N, N) }
@noinline
function into(n: N, own acc: i32[]): i32[] {
    match (n) {
        L(x) => { acc = acc.append(x); return acc; },
        B(l, r) => { acc = into(l, acc); acc = into(r, acc); return acc; },
    }
}
@noinline
function idx(xs: i32[], i: i32, own acc: i32[]): i32[] {
    if (i >= xs.len()) { return acc; }
    acc = acc.append(xs[i]);
    acc = idx(xs, i + 1, acc);
    return acc;
}
function build(d: i32): N { if (d == 0) { return L(1); } return B(build(d - 1), build(d - 1)); }
`

const ownAccTreeBody = ownAccRecursionPrelude + `
function round(i: i32): i32 {
    let t: N = build(4);
    let acc: i32[] = [];
    acc = into(t, acc);
    return acc.len();
}
`

const ownAccIndexBody = ownAccRecursionPrelude + `
function round(i: i32): i32 {
    let chunk: i32[] = [1, 2, 3, 4, 5, 6, 7, 8];
    let acc: i32[] = [];
    acc = idx(chunk, 0, acc);
    return acc.len();
}
`

func ownAccTreeSrc(rounds int) string  { return ownAccTreeBody + ownAccMain(rounds, rounds*16) }
func ownAccIndexSrc(rounds int) string { return ownAccIndexBody + ownAccMain(rounds, rounds*8) }

func ownAccMain(rounds, want int) string {
	return `function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < ` + strconv.Itoa(rounds) + `) { acc = acc + round(r); r = r + 1; }
    if (acc != ` + strconv.Itoa(want) + `) { return 1; }
    if (__rc_underflow_count() != 0) { return 2; }
    return 0;
}`
}

func TestX86_64OwnAccumulatorRecursionReclaim(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  func(int) string
	}{
		{"own_acc_tree_recursion", ownAccTreeSrc},
		{"own_acc_index_recursion", ownAccIndexSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, rounds := range []int{100, 200, 400} {
				name := tc.name + "/" + strconv.Itoa(rounds)
				allocs, frees, live := leakCounts(t, name, tc.src(rounds), 0)
				if live != 0 || allocs != frees {
					t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census — "+
						"an own accumulator threaded through recursion must hand its buffer back without stranding a reference",
						name, allocs, frees, live)
				}
			}
		})
	}
}

func ownAccBumpSrc(body string, rounds int) string {
	return body + `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let r: i32 = 0;
    let sum: i32 = 0;
    while (r < ` + strconv.Itoa(rounds) + `) { sum = sum + round(r); r = r + 1; }
    if (sum == 0) { return 201; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestWASMOwnAccumulatorRecursionBounded(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"own_acc_tree_recursion", ownAccTreeBody},
		{"own_acc_index_recursion", ownAccIndexBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			small := runWasm(t, ownAccBumpSrc(tc.body, 20))
			large := runWasm(t, ownAccBumpSrc(tc.body, 200))
			if small == 201 || large == 201 {
				t.Fatalf("value-incorrect run: small=%d large=%d", small, large)
			}
			if small != large {
				t.Errorf("own accumulator recursion must be O(1) heap, got rounds=20 -> %d, rounds=200 -> %d (leak)", small, large)
			}
			if small == 0 {
				t.Errorf("expected a non-zero working set, got 0 (probe not exercising the heap)")
			}
		})
	}
}
