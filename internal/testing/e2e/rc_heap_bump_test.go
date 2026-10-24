package e2e

import (
	"testing"
)

func heapBumpGrowthSrc(n string) string {
	return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        let row: i32[] = [i, i + 1, i + 2];
        sum = sum + row[0];
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const heapBumpZeroSrc = `function main(): i32 {
    return (__heap_bump_bytes() as i32);
}`

func TestX86_64HeapBumpBounded(t *testing.T) {
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
