package e2eselfhost

import (
	"fmt"
	"strings"
	"testing"
)

// --- The heap-bump FLATNESS gate on the self-host IR path (#4365) ------------
//
// Native pins ~50 reclaim shapes with one probe: run the same program at a small
// and a large iteration count and require `__heap_bump_bytes()` to report the
// SAME high-water mark for both. A shape that reclaims is flat; a shape that
// leaks tracks the iteration count. The self-host had one heap-bump test and it
// pins the BUILTIN (`self_host_heap_bump_bytes_ir_test.go`), not any shape's
// flatness — so every reclaim behaviour below was unasserted on this path, and
// the fixpoint is structurally blind to a stable over-allocation (a compiler
// that leaks identically in both generations still reproduces itself).
//
// Each row is measured against the compiler's own reclaim, not against native's
// number: the two allocate different amounts for the same program (different
// box sizes and temp strategies), so the contract is FLATNESS, with native
// asserted flat alongside as the oracle that the shape is reclaimable at all.
//
// Rows deliberately absent:
//
//   - An enum scrutinee that is never bound (`match (mk(i)) { … }`) is NOT flat
//     on either compiler — self-host 160 -> 128 and native 64 -> 0 at N=50/200,
//     both wrapping a growing byte count through the exit-code byte. That is
//     #6393, an open leak on all three backends rather than a self-host gap, so
//     gating it here would pin a bug rather than a behaviour.
//   - A string-array element built from literals reports an exact 0 on the
//     self-host: it allocates nothing, so flatness holds vacuously and the row
//     would gate nothing. Every row below is required to allocate.
type heapBumpFlatCase struct {
	name string
	src  func(n string) string
}

var heapBumpFlatCases = []heapBumpFlatCase{
	// A concat chain's intermediates: three temps per iteration, all dead at the
	// end of the statement.
	{"nested-concat", func(n string) string {
		return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var a: string = "longer_string_one_here";
    var b: string = "longer_string_two_here";
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + (a + b + a + b).len(); i = i + 1; }
    return ((__heap_bump_bytes() as i32) - before) + (acc - acc);
}`
	}},
	// The receiver of a borrowing method is still a temp the caller owns.
	{"len-receiver", func(n string) string {
		return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var a: string = "hello there friend, ";
    var b: string = "general kenobi!!!";
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + (a + b).len(); i = i + 1; }
    if (acc < 0) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	// An array literal evaluated as a STATEMENT: nothing ever reads it, so
	// nothing but the drop insertion can free it.
	{"stmt-temp", func(n string) string {
		return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { [i, i + 1, i + 2]; i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	// A discarded call RESULT, in both of the shapes that carry rc: a struct box
	// and an array buffer.
	{"discarded-call-struct", func(n string) string {
		return `struct P { x: i32, y: i32 }
function mk(v: i32): P { return P { x: v, y: v }; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	{"discarded-call-arr", func(n string) string {
		return `function mk(v: i32): i32[] { return [v, v + 1, v + 2]; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	// A fresh array handed to a BORROWING callee: the caller keeps the only
	// reference and must release it after the call returns.
	{"call-arg-temp", func(n string) string {
		return `function sum3(xs: i32[]): i32 { return xs[0] + xs[1] + xs[2]; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + sum3([i, i + 1, i + 2]); i = i + 1; }
    if (acc < 0) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	// A `__alloc_u8` buffer rebound through `.with`, which supersedes the old
	// buffer once per call.
	{"literal-alloc", func(n string) string {
		return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        var b: u8[] = __alloc_u8(8);
        b = b.with(0, (i % 200) as u8);
        acc = acc + (b[0] as i32);
        i = i + 1;
    }
    if (acc < 0) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	// A struct local rebound to a fresh literal: the superseded box AND its
	// replaced string field both have to go (the #4355 / #6703 admission).
	{"replaced-field", func(n string) string {
		return `struct S { name: string, n: i32 }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var o: S = S { name: "seed", n: 0 };
    var i: i32 = 0;
    while (i < ` + n + `) { o = S { name: "ab" + "cd", n: i }; i = i + 1; }
    if (o.name.len() != 4) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	// A tuple box bound to a loop-scoped local.
	{"tuple-temp", func(n string) string {
		return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) { var t: (i32, i32) = (i, i + 1); acc = acc + t.0 + t.1; i = i + 1; }
    if (acc < 0) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	// A map LOOKUP. The Option box `m.get(k)` builds is consumed by the match
	// and nothing else can reach it, and the fresh string-literal key is not
	// retained by the lookup. Both edges are covered because they were separate
	// leaks: a HIT carries a payload out of the map, a MISS does not (#6875).
	{"map-get", func(n string) string {
		return `import "core/map";
function main(): i32 {
    var index: Map[string, i32] = map_new(64);
    index = index.insert("alpha", 1);
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        match (index.get("alpha")) { Some(g) => { acc = acc + g; }, None => { acc = acc - 1; } }
        match (index.get("absent")) { Some(g) => { acc = acc + g; }, None => { acc = acc + 2; } }
        i = i + 1;
    }
    if (acc < 0) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
	// A nested array: the outer buffer's drop has to walk its elements.
	{"nested-array", func(n string) string {
		return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) { var g: i32[][] = [[i, i + 1], [i + 2]]; acc = acc + g[0].len() + g[1].len(); i = i + 1; }
    if (acc < 0) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}},
}

// The two iteration counts. A leak of one box per iteration moves the mark by
// 150 * (box size) between them, which no wrap can hide as equality.
const (
	heapBumpFlatSmallN = "50"
	heapBumpFlatLargeN = "200"
)

// TestSelfHostHeapBumpFlatIRX86_64 — small-N == large-N for every shape, on the
// binary the SELF-HOST compiler emits.
//
// The exit code carries the byte delta, so it is read modulo 256; that is what
// makes this an equality test between two runs of the same program rather than
// an absolute assertion. A leak that happened to be an exact multiple of 256
// per 150 iterations would evade it, which is why the small-N run must also be
// non-zero: a shape that allocates nothing cannot fail, and a row that cannot
// fail is not a gate.
func TestSelfHostHeapBumpFlatIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range heapBumpFlatCases {
		t.Run(tc.name, func(t *testing.T) {
			run := func(n string) int {
				src := tc.src(n) + "\n"
				asm := hevCompile(t, runner, driverBin, src, nil)
				bin := buildBin(t, gcc, dir, fmt.Sprintf("hbf_%s_%s", strings.ReplaceAll(tc.name, "-", "_"), n), asm)
				_, exit := hevRun(t, runner, bin)
				return exit
			}
			small, large := run(heapBumpFlatSmallN), run(heapBumpFlatLargeN)
			if small == 0 {
				t.Fatalf("%s allocated nothing at N=%s — the probe is not exercising the path, so the "+
					"flatness below would hold vacuously", tc.name, heapBumpFlatSmallN)
			}
			if small != large {
				t.Errorf("%s is not flat: N=%s bumped %d bytes, N=%s bumped %d — the mark tracks the "+
					"iteration count, so the shape strands one allocation per round",
					tc.name, heapBumpFlatSmallN, small, heapBumpFlatLargeN, large)
			}
			// Native is the oracle that the shape is reclaimable at all: if it
			// stops being flat there, this row is pinning a whole-project leak
			// rather than a self-host one, and the row (not the compiler) is
			// what needs revisiting.
			_, nsmall := compileAndRunX86_64(t, tc.src(heapBumpFlatSmallN)+"\n")
			_, nlarge := compileAndRunX86_64(t, tc.src(heapBumpFlatLargeN)+"\n")
			if nsmall != nlarge {
				t.Errorf("%s is not flat on NATIVE either (%d vs %d) — the row is gating a leak both "+
					"compilers have, not a self-host divergence", tc.name, nsmall, nlarge)
			}
		})
	}
}
