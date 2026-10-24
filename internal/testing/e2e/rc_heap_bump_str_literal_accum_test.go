package e2e

import (
	"testing"
)

func heapBumpStrLiteralAccumSrc(n string) string {
	return `function main(): i32 {
    let p: string = "0123456789abcdefghij";
    let s: string = "";
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) {
        s = p + "!";
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestWASMHeapBumpStrLiteralAccumBounded(t *testing.T) {
	small := runWasm(t, heapBumpStrLiteralAccumSrc("20000"))
	large := runWasm(t, heapBumpStrLiteralAccumSrc("80000"))
	if small != large {
		t.Errorf("literal-init string accumulator should reclaim (bounded bump): N=20000 -> %d, N=80000 -> %d (a leak grows ~4x with N)", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water (the accumulator does heap-allocate), got 0")
	}
}
