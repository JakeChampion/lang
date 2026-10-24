package e2e

import (
	"testing"
)

func destructureBumpGrowthSrc(n string) string {
	return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        let p: (i32[], i32) = ([i, i + 1, i + 2], i);
        let (a, b) = p;
        sum = sum + a[0] + b;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const destructureUnderflowSrc = `function main(): i32 {
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < 200) {
        let p: (i32[], i32) = ([i, i + 1, i + 2], i);
        let (a, b) = p;
        sum = sum + a[0] + b;
        i = i + 1;
    }
    return __rc_underflow_count();
}`

func TestX86_64DestructureHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, destructureBumpGrowthSrc("50"))
	large := mustRunX86_64FreeOn(t, destructureBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("destructure bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunX86_64FreeOn(t, destructureUnderflowSrc); code != 0 {
		t.Errorf("destructure over-releases = %d, want 0", code)
	}
}

func TestArm64DestructureHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, destructureBumpGrowthSrc("50"))
	large := mustRunArm64FreeOn(t, destructureBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("destructure bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunArm64FreeOn(t, destructureUnderflowSrc); code != 0 {
		t.Errorf("destructure over-releases = %d, want 0", code)
	}
}

func TestWASMDestructureHeapBumpBounded(t *testing.T) {
	small := runWasm(t, destructureBumpGrowthSrc("50"))
	large := runWasm(t, destructureBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("destructure bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, destructureUnderflowSrc); got != 0 {
		t.Errorf("destructure over-releases = %d, want 0", got)
	}
}

func nestedDestructureBumpGrowthSrc(n string) string {
	return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        let p: (i32, (i32[], i32)) = (i, ([i, i + 1, i + 2], i));
        let (x, (a, b)) = p;
        sum = sum + x + a[0] + b;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const nestedDestructureUnderflowSrc = `function main(): i32 {
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < 200) {
        let p: (i32, (i32[], i32)) = (i, ([i, i + 1, i + 2], i));
        let (x, (a, b)) = p;
        sum = sum + x + a[0] + b;
        i = i + 1;
    }
    return __rc_underflow_count();
}`

func TestX86_64NestedDestructureHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, nestedDestructureBumpGrowthSrc("50"))
	large := mustRunX86_64FreeOn(t, nestedDestructureBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("nested destructure bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leaked inner box would grow with N)", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunX86_64FreeOn(t, nestedDestructureUnderflowSrc); code != 0 {
		t.Errorf("nested destructure over-releases = %d, want 0", code)
	}
}

func TestArm64NestedDestructureHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, nestedDestructureBumpGrowthSrc("50"))
	large := mustRunArm64FreeOn(t, nestedDestructureBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("nested destructure bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunArm64FreeOn(t, nestedDestructureUnderflowSrc); code != 0 {
		t.Errorf("nested destructure over-releases = %d, want 0", code)
	}
}

func TestWASMNestedDestructureHeapBumpBounded(t *testing.T) {
	small := runWasm(t, nestedDestructureBumpGrowthSrc("50"))
	large := runWasm(t, nestedDestructureBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("nested destructure bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, nestedDestructureUnderflowSrc); got != 0 {
		t.Errorf("nested destructure over-releases = %d, want 0", got)
	}
}
