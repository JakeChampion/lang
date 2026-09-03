package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #6425 — `acc = step(acc, x)` on a string-bearing array: the callee appends
// and hands the (possibly regrown) buffer back, and the caller's overwrite of
// `acc` superseded the old buffer without releasing it, one buffer per grow.
// Measured on the issue's own commit at 352 B a round; the array-overwrite
// depth and identity-guard work (docs/rc-log/2026-09-01-array-overwrite-depth.md)
// closed it, and this pins the shape so it cannot reopen. Rounds-based: the
// accumulator is built and dies each round, so a correct runtime is flat in
// the round count.

const threadedStringArrayAccumulatorSrc = `import "std/i32";
@noinline
function cb(xs: string[], i: i32): string[] { return xs.append("p-a-wide-payload-past-any-inline-threshold" + i.to_string()); }
@noinline
function round(): i32 {
    var g: string[] = [];
    var i: i32 = 0;
    while (i < 16) { g = cb(g, i); i = i + 1; }
    return g.len() - 16;
}
function main(): i32 {
    var r: i32 = 0;
    var acc: i32 = 0;
    while (r < 200) { acc = acc + round(); r = r + 1; }
    return acc;
}`

func threadedStringArrayAccumulatorBumpSrc(n string) string {
	return `import "std/i32";
@noinline
function cb(xs: string[], i: i32): string[] { return xs.append("p-a-wide-payload-past-any-inline-threshold" + i.to_string()); }
@noinline
function round(): i32 {
    var g: string[] = [];
    var i: i32 = 0;
    while (i < 16) { g = cb(g, i); i = i + 1; }
    return g.len() - 16;
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var r: i32 = 0;
    var acc: i32 = 0;
    while (r < ` + n + `) { acc = acc + round(); r = r + 1; }
    if (acc != 0) { return 99; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestX86_64ThreadedStringArrayAccumulatorReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, threadedStringArrayAccumulatorSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (strings and buffers), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("threaded accumulator leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
	small := mustRunX86_64FreeOn(t, threadedStringArrayAccumulatorBumpSrc("20"))
	large := mustRunX86_64FreeOn(t, threadedStringArrayAccumulatorBumpSrc("400"))
	if small != large {
		t.Errorf("accumulator bump should be flat in rounds: 20 -> %d, 400 -> %d", small, large)
	}
}

func TestArm64ThreadedStringArrayAccumulatorReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, threadedStringArrayAccumulatorSrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (strings and buffers), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("threaded accumulator leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
	small := mustRunArm64FreeOn(t, threadedStringArrayAccumulatorBumpSrc("20"))
	large := mustRunArm64FreeOn(t, threadedStringArrayAccumulatorBumpSrc("400"))
	if small != large {
		t.Errorf("accumulator bump should be flat in rounds: 20 -> %d, 400 -> %d", small, large)
	}
}

func TestWASMThreadedStringArrayAccumulatorReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, threadedStringArrayAccumulatorBumpSrc("20"))
	large := runWasm(t, threadedStringArrayAccumulatorBumpSrc("400"))
	if small != large {
		t.Errorf("accumulator bump should be flat in rounds: 20 -> %d, 400 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
}
