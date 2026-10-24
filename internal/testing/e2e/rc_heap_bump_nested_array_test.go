package e2e

import (
	"testing"
)

func nestedArrBumpSrc(n string) string {
	return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        let g: i32[][] = [[i, i + 1], [i + 2, i + 3]];
        acc = acc + g[0][1] + g[1][0];
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const nestedArrUnderflowSrc = `function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        let g: i32[][] = [[i, i + 1, i + 2], [i + 3, i + 4]];
        acc = acc + g[0][2] + g[1][1];
        i = i + 1;
    }
    // sum over i of ((i+2) + (i+4)) = 2i+6, i=0..199 => 2*19900 + 1200 = 41000
    if (acc != 41000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64NestedArrayReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, nestedArrBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, nestedArrBumpSrc("5000"))
	if small != large {
		t.Errorf("nested-array bump should be bounded (inner reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunX86_64FreeOn(t, nestedArrUnderflowSrc); code != 0 {
		t.Errorf("nested-array reclaim: code=%d (999=value mismatch, >0=over-release)", code)
	}
}

func TestArm64NestedArrayReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, nestedArrBumpSrc("50"))
	large := mustRunArm64FreeOn(t, nestedArrBumpSrc("5000"))
	if small != large {
		t.Errorf("nested-array bump should be bounded (inner reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunArm64FreeOn(t, nestedArrUnderflowSrc); code != 0 {
		t.Errorf("nested-array reclaim: code=%d", code)
	}
}

func TestWASMNestedArrayReclaim(t *testing.T) {
	small := runWasm(t, nestedArrBumpSrc("50"))
	large := runWasm(t, nestedArrBumpSrc("5000"))
	if small != large {
		t.Errorf("nested-array bump should be bounded (inner reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, nestedArrUnderflowSrc); got != 0 {
		t.Errorf("nested-array reclaim: got %d", got)
	}
}
