package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Phase 6 measurement — the __heap_bump_bytes() probe returns the bump
// allocator's high-water mark (cursor − region base). The cursor only
// advances on a fresh bump and never on a freelist reuse, so a loop that
// reclaims each iteration's allocation keeps the mark FLAT regardless of
// iteration count, while a leaking loop grows it linearly.
//
// These tests turn that into a real "the leak is fixed" regression — the
// soundness tests (rc_loop_var_test, rc_freelist_test) only prove no
// over-release; this proves the buffers are actually reclaimed+reused to
// a BOUNDED high-water mark. Two programs differing only in iteration
// count must report the SAME bump growth; if reclaim regressed (the
// Phase 5h / push-loop dec-on-overwrite stopped freeing), the larger run
// would grow proportionally and the counts would diverge.

// heapBumpGrowthSrc builds a program whose main returns the bytes the
// bump cursor advanced across an n-iteration build-and-discard loop.
func heapBumpGrowthSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var row: i32[] = [i, i + 1, i + 2];
        sum = sum + row[0];
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// heapBumpZeroSrc never allocates, so the probe must read 0.
const heapBumpZeroSrc = `function main(): i32 {
    return (__heap_bump_bytes() as i32);
}`

func TestX86_64HeapBumpBounded(t *testing.T) {
	// No allocation → 0.
	if _, code := compileAndRunX86_64FreeOn(t, heapBumpZeroSrc); code != 0 {
		t.Errorf("no-alloc program should report 0 bump bytes, got %d", code)
	}
	small := mustRunX86_64FreeOn(t, heapBumpGrowthSrc("50"))
	large := mustRunX86_64FreeOn(t, heapBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water (one block), got 0")
	}
}

func TestArm64HeapBumpBounded(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, heapBumpZeroSrc); code != 0 {
		t.Errorf("no-alloc program should report 0 bump bytes, got %d", code)
	}
	small := mustRunArm64FreeOn(t, heapBumpGrowthSrc("50"))
	large := mustRunArm64FreeOn(t, heapBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water (one block), got 0")
	}
}

func TestWASMHeapBumpBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, heapBumpZeroSrc); got != 0 {
		t.Errorf("no-alloc program should report 0 bump bytes, got %d", got)
	}
	small := runWasm(t, heapBumpGrowthSrc("50"))
	large := runWasm(t, heapBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water (one block), got 0")
	}
}

func mustRunX86_64FreeOn(t *testing.T, src string) int {
	t.Helper()
	_, code := compileAndRunX86_64FreeOn(t, src)
	return code
}

func mustRunArm64FreeOn(t *testing.T, src string) int {
	t.Helper()
	_, code := compileAndRunArm64FreeOn(t, src)
	return code
}
