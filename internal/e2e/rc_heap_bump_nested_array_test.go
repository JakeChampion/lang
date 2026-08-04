package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Nested-array (array-of-array) inner-buffer reclamation (RC-Perceus).
// `i32[][]` is array-of-rc-element (arrElemIsRcTracked(ArrayType) is
// true), so before this slice the outer drop freed only the OUTER buffer
// — the exit sweep's __fern_drop_arr_ptr flat-rc_dec'd each element and
// emitVarReinitDropOld's plain __fern_arr_dec ignored them, so every
// INNER buffer leaked. A `var g = [[..],[..]]` loop grew unbounded (the
// profiling probe measured 3264 B → 320064 B). The fix routes an
// array-of-(primitive-array) drop through the generated
// __drop_arr_arr_<innerStride> loop (free each inner buffer via
// __fern_arr_dec, then the outer), so the bump high-water stays FLAT.

func nestedArrBumpSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        var g: i32[][] = [[i, i + 1], [i + 2, i + 3]];
        acc = acc + g[0][1] + g[1][0];
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Inner buffers must reclaim AND not over-release (a shared inner array
// is is_unique-gated). Returns 0 iff value-correct and no over-release.
const nestedArrUnderflowSrc = `function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var g: i32[][] = [[i, i + 1, i + 2], [i + 3, i + 4]];
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
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
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
