package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #8836 — `acc = put(acc, piece)` on a string LOCAL: the callee hands a
// string back that may or may not be the buffer it was given, and the
// caller's overwrite of `acc` must release the superseded buffer exactly
// when one was superseded. Measured at 8 KB live per 1 000 iterations and
// OOM at 100k when filed; both callee shapes are balanced now, and this pins
// them. Rounds-based: the accumulator is built and dies each round, so a
// correct runtime is flat in the round count.
//
// The first two callees are the two answers a callee can give: `put` appends
// to its parameter and hands the SAME buffer back (grown in place when it is
// unique), `fresh` builds a new string and hands THAT back, so the caller's
// old buffer is superseded on every iteration.
// The third callee takes the accumulator as an `own` parameter. That moves
// the caller's reference in, and the caller's binding then holds only what
// the callee handed back; on x86-64 the single-word string argument taint
// still read the move as a possible retention and kept the binding out of
// the exit sweep, so the final accumulator of every frame was stranded —
// balanced only when the frame returned it.
const threadedStringAccumulatorCallees = `@noinline
function put(a: string, s: string): string { a = a + s; return a; }
@noinline
function fresh(a: string, s: string): string { var b: string = a + s; return b; }
@noinline
function take(own a: string, s: string): string { return a + s; }
@noinline
function round_put(): i32 {
    var acc: string = "";
    var i: i32 = 0;
    while (i < 16) { acc = put(acc, "12345678"); i = i + 1; }
    return acc.len() - 128;
}
@noinline
function round_fresh(): i32 {
    var acc: string = "";
    var i: i32 = 0;
    while (i < 16) { acc = fresh(acc, "12345678"); i = i + 1; }
    return acc.len() - 128;
}
@noinline
function round_take(): i32 {
    var acc: string = "";
    var i: i32 = 0;
    while (i < 16) { acc = take(acc, "12345678"); i = i + 1; }
    return acc.len() - 128;
}
`

const threadedStringAccumulatorSrc = threadedStringAccumulatorCallees + `function main(): i32 {
    var r: i32 = 0;
    var acc: i32 = 0;
    while (r < 200) { acc = acc + round_put() + round_fresh() + round_take(); r = r + 1; }
    return acc;
}`

func threadedStringAccumulatorBumpSrc(n string) string {
	return threadedStringAccumulatorCallees + `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var r: i32 = 0;
    var acc: i32 = 0;
    while (r < ` + n + `) { acc = acc + round_put() + round_fresh() + round_take(); r = r + 1; }
    if (acc != 0) { return 99; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestX86_64ThreadedStringAccumulatorReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, threadedStringAccumulatorSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (string buffers), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("threaded string accumulator leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
	small := mustRunX86_64FreeOn(t, threadedStringAccumulatorBumpSrc("20"))
	large := mustRunX86_64FreeOn(t, threadedStringAccumulatorBumpSrc("400"))
	if small != large {
		t.Errorf("accumulator bump should be flat in rounds: 20 -> %d, 400 -> %d", small, large)
	}
}

func TestArm64ThreadedStringAccumulatorReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, threadedStringAccumulatorSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (string buffers), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("threaded string accumulator leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
	small := mustRunArm64FreeOn(t, threadedStringAccumulatorBumpSrc("20"))
	large := mustRunArm64FreeOn(t, threadedStringAccumulatorBumpSrc("400"))
	if small != large {
		t.Errorf("accumulator bump should be flat in rounds: 20 -> %d, 400 -> %d", small, large)
	}
}

func TestWASMThreadedStringAccumulatorReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, threadedStringAccumulatorBumpSrc("20"))
	large := runWasm(t, threadedStringAccumulatorBumpSrc("400"))
	if small != large {
		t.Errorf("accumulator bump should be flat in rounds: 20 -> %d, 400 -> %d", small, large)
	}
}
