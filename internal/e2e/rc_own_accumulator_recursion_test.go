package e2e

import (
	"strconv"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// An `own` array accumulator threaded through RECURSION — `acc = into(l,
// acc); acc = into(r, acc); return acc;` — leaked one buffer per call whenever
// the recursion's other argument was borrow-tainted: rhsTainted's
// any-tainted-arg rule tainted the call result, `acc` lost freeEligible, and
// every `return acc` retained as a borrow does with no sweep dec to balance
// it. The stranded reference held the buffer at rc 2, so the next append after
// a call boundary copied the whole accumulator and orphaned the source — one
// full copy per leaf (128 blocks for 128 leaves; 100 for 100 outer calls of the
// index-recursive form). The flat-loop accumulator never had the problem, and
// the same-shaped std/ordmap walker only escaped it because its one-payload
// enum is owned by default, which left nothing tainted to propagate.
//
// findReturnsFreshBox now credits an `own` param that is only ever rebound
// from owned values, so the recursion's result is the caller's own box and
// `acc` stays freeEligible (internal/ir/own_accumulator_fresh_return_test.go).
//
// Each round rebuilds the accumulator from scratch, so a per-call leak scales
// with the round count; three counts tell that apart from a fixed startup
// cost, and the exit code folds in `__rc_underflow_count()` because a census
// is blind to an over-release.
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

// ownAccTreeBody: the tree walker over a borrowed NON-uniform enum (`L` and
// `B` boxes differ in size, so N is not owned by default and `l` / `r` carry
// the scrutinee's borrow taint into the recursion's arguments). 16 leaves.
const ownAccTreeBody = ownAccRecursionPrelude + `
function round(i: i32): i32 {
    var t: N = build(4);
    var acc: i32[] = [];
    acc = into(t, acc);
    return acc.len();
}
`

// ownAccIndexBody: index recursion over a borrowed array — the borrowed `xs`
// is the tainted argument. 8 elements.
const ownAccIndexBody = ownAccRecursionPrelude + `
function round(i: i32): i32 {
    var chunk: i32[] = [1, 2, 3, 4, 5, 6, 7, 8];
    var acc: i32[] = [];
    acc = idx(chunk, 0, acc);
    return acc.len();
}
`

func ownAccTreeSrc(rounds int) string  { return ownAccTreeBody + ownAccMain(rounds, rounds*16) }
func ownAccIndexSrc(rounds int) string { return ownAccIndexBody + ownAccMain(rounds, rounds*8) }

func ownAccMain(rounds, want int) string {
	return `function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
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

// The wasm leg has no alloc census; the bump high-water mark is the same
// signal — one round's working set, equal for 20 and 200 rounds when every
// stranded copy is reclaimed.
func ownAccBumpSrc(body string, rounds int) string {
	return body + `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var r: i32 = 0;
    var sum: i32 = 0;
    while (r < ` + strconv.Itoa(rounds) + `) { sum = sum + round(r); r = r + 1; }
    if (sum == 0) { return 201; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestWASMOwnAccumulatorRecursionBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
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
