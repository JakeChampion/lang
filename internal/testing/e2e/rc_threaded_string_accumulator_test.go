package e2e

import (
	"testing"
)

const threadedStringAccumulatorCallees = `@noinline
function put(a: string, s: string): string { a = a + s; return a; }
@noinline
function fresh(a: string, s: string): string { let b: string = a + s; return b; }
@noinline
function take(own a: string, s: string): string { return a + s; }
@noinline
function round_put(): i32 {
    let acc: string = "";
    let i: i32 = 0;
    while (i < 16) { acc = put(acc, "12345678"); i = i + 1; }
    return acc.len() - 128;
}
@noinline
function round_fresh(): i32 {
    let acc: string = "";
    let i: i32 = 0;
    while (i < 16) { acc = fresh(acc, "12345678"); i = i + 1; }
    return acc.len() - 128;
}
@noinline
function round_take(): i32 {
    let acc: string = "";
    let i: i32 = 0;
    while (i < 16) { acc = take(acc, "12345678"); i = i + 1; }
    return acc.len() - 128;
}
`

const threadedStringAccumulatorSrc = threadedStringAccumulatorCallees + `function main(): i32 {
    let r: i32 = 0;
    let acc: i32 = 0;
    while (r < 200) { acc = acc + round_put() + round_fresh() + round_take(); r = r + 1; }
    return acc;
}`

func threadedStringAccumulatorBumpSrc(n string) string {
	return threadedStringAccumulatorCallees + `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let r: i32 = 0;
    let acc: i32 = 0;
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
	small := runWasm(t, threadedStringAccumulatorBumpSrc("20"))
	large := runWasm(t, threadedStringAccumulatorBumpSrc("400"))
	if small != large {
		t.Errorf("accumulator bump should be flat in rounds: 20 -> %d, 400 -> %d", small, large)
	}
}
