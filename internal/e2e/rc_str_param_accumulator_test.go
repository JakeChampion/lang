package e2e

import (
	"fmt"
	"testing"
)

// A string PARAMETER that the body reassigns is consumed-threaded (#8785):
// the entry retain hands the frame a reference of its own, the reassignment's
// dec-on-overwrite releases what the frame owns, and the exit sweep spends the
// last count. Two things follow, and each has its own gate here.
//
//   - The frame no longer leaks. Before the promotion the sweep skipped a
//     borrowed param, so the value a reassignment put into that slot was
//     never released: ONE heap string per call, unbounded, with no append
//     involved (`a = s + s` leaked identically).
//   - An accumulator whose loop runs inside the callee is linear. The first
//     append copies — it must, the incoming buffer is the caller's, and the
//     entry retain's rc 2 is what sends it down __fern_str_append's copy
//     path — and every later one grows the replacement in place at rc 1.
//
// The aliasing half (that the caller's string is NOT lengthened under it)
// lives in rcCorpus, which runs on all three backends; these two gates are
// x86-64 + wasm, the ABIs with a __fern_str_append at all (arm64 has none,
// #8414, so its codegen for the append is unchanged).

// strParamAccumulatorSrc: the parameter IS the accumulator. n appends of 8
// bytes with the loop inside the callee.
func strParamAccumulatorSrc(n int) string {
	return fmt.Sprintf(`function grow(a: string, n: i32): string {
    var i: i32 = 0;
    while (i < n) { a = a + "12345678"; i = i + 1; }
    return a;
}
function main(): i32 {
    if (grow("", %d).len() != %d) { return 2; }
    return 0;
}`, n, n*8)
}

// strParamReassignNoAppendSrc: the leak half on its own — the reassignment's
// RHS is a fresh concat of the OTHER parameter, so no self-append, no
// in-place growth, nothing but the ownership of the overwritten slot.
const strParamReassignNoAppendSrc = `function f(a: string, s: string): i32 {
    a = s + s;
    return a.len();
}
function main(): i32 {
    var base: string = "abcdefgh" + "ijklmnop";
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 512) { t = t + f(base, "12345678"); i = i + 1; }
    if (t != 512 * 16) { return 2; }
    return 0;
}`

func checkStrParamNoLeak(t *testing.T, what, stderr string, exit int) {
	t.Helper()
	if exit != 0 {
		t.Fatalf("%s: exit %d, want 0; stderr: %s", what, exit, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	t.Logf("%s: allocs=%d frees=%d live_bytes=%d", what, allocs, frees, live)
	if allocs == 0 {
		t.Fatalf("%s: no allocations — the probe is not building anything", what)
	}
	if allocs != frees || live != 0 {
		t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — a reassigned string parameter must not strand the value it was overwritten with", what, allocs, frees, live)
	}
}

// checkStrParamAccumulatorShape is the shape gate: the same accumulation at
// two sizes must cost proportionally. Quadratic copying allocates once per
// append (allocs == n), so the per-size bound n/8 already separates the
// regimes; the ratio check is the second size earning its place — under the
// copy regime doubling n doubles the allocations too, so only the growth
// SCHEDULE (one allocation per allocator class step) keeps 2n under 1.5x n.
func checkStrParamAccumulatorShape(t *testing.T, small, large [3]int64, n int) {
	t.Helper()
	for _, c := range []struct {
		n   int
		got [3]int64
	}{{n, small}, {2 * n, large}} {
		if c.got[0] > int64(c.n/8) {
			t.Errorf("%d appends: allocs=%d, want <= %d — the accumulator is copied per append rather than grown in place", c.n, c.got[0], c.n/8)
		}
		if c.got[0] != c.got[1] || c.got[2] != 0 {
			t.Errorf("%d appends: allocs=%d frees=%d live_bytes=%d — the in-place grows must leave the census balanced", c.n, c.got[0], c.got[1], c.got[2])
		}
	}
	if small[0] == 0 {
		t.Fatal("no allocations: the probe is not building its string")
	}
	if large[0] > small[0]*3/2 {
		t.Errorf("allocs %d -> %d for %d -> %d appends: doubling the work more than 1.5x'd the allocations, which is the copy regime, not the growth schedule", small[0], large[0], n, 2*n)
	}
}

func TestX86_64StrParamAccumulatorInPlace(t *testing.T) {
	const n = 4096
	var counts [2][3]int64
	for i, size := range []int{n, 2 * n} {
		_, stderr, exit := runLeakCheckX86_64(t, strParamAccumulatorSrc(size))
		if exit != 0 {
			t.Fatalf("%d appends: exit %d, want 0; stderr: %s", size, exit, stderr)
		}
		a, f, live := parseLeakCheckLine(t, stderr)
		counts[i] = [3]int64{a, f, live}
		t.Logf("%d appends: allocs=%d frees=%d live_bytes=%d", size, a, f, live)
	}
	checkStrParamAccumulatorShape(t, counts[0], counts[1], n)
}

func TestWASMStrParamAccumulatorInPlace(t *testing.T) {
	const n = 4096
	var counts [2][3]int64
	for i, size := range []int{n, 2 * n} {
		_, stderr, exit := runLeakCheckWasm(t, strParamAccumulatorSrc(size), false)
		if exit != 0 {
			t.Fatalf("%d appends: exit %d, want 0; stderr: %s", size, exit, stderr)
		}
		a, f, live := parseLeakCheckLine(t, stderr)
		counts[i] = [3]int64{a, f, live}
		t.Logf("%d appends: allocs=%d frees=%d live_bytes=%d", size, a, f, live)
	}
	checkStrParamAccumulatorShape(t, counts[0], counts[1], n)
}

func TestX86_64StrParamReassignNoLeak(t *testing.T) {
	_, stderr, exit := runLeakCheckX86_64(t, strParamReassignNoAppendSrc)
	checkStrParamNoLeak(t, "x86-64 string param reassign", stderr, exit)
}

func TestWASMStrParamReassignNoLeak(t *testing.T) {
	_, stderr, exit := runLeakCheckWasm(t, strParamReassignNoAppendSrc, false)
	checkStrParamNoLeak(t, "wasm string param reassign", stderr, exit)
}
