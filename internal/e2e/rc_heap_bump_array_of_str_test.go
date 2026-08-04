package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Array-of-string[] (`string[][]`) inner-buffer reclamation (RC-Perceus).
// The nested-array slice handled only PRIMITIVE inner arrays; a
// `string[][]` kept the flat __fern_drop_arr_ptr, freeing the outer
// buffer but leaking each inner string[] buffer AND its strings
// (profiling probe: 3264 B → 320064 B). This routes the array-of-array
// drop with a STRING inner through a generated __drop_arr_arr_str loop:
// per outer element, reclaim the inner string[] via the ABI-correct
// helper (__fern_drop_arr_str on two-word wasm/arm64 — walk + str_dec +
// free; __fern_drop_arr_ptr on native single-word x86_64), then free the
// outer buffer. Each helper is_unique-gates internally.

func arrOfStrBumpSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        var g: string[][] = [["aa", "bb"], ["cc"]];
        acc = acc + g[0][1].len();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Inner strings + buffers must reclaim AND not over-release.
const arrOfStrUnderflowSrc = `function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var g: string[][] = [["alpha", "beta"], ["gamma"]];
        acc = acc + g[0][0].len() + g[1][0].len();
        i = i + 1;
    }
    // ("alpha"=5 + "gamma"=5) * 200 = 2000
    if (acc != 2000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64ArrayOfStrReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, arrOfStrBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, arrOfStrBumpSrc("5000"))
	if small != large {
		t.Errorf("string[][] bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunX86_64FreeOn(t, arrOfStrUnderflowSrc); code != 0 {
		t.Errorf("string[][] reclaim: code=%d (999=value mismatch, >0=over-release)", code)
	}
}

func TestArm64ArrayOfStrReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, arrOfStrBumpSrc("50"))
	large := mustRunArm64FreeOn(t, arrOfStrBumpSrc("5000"))
	if small != large {
		t.Errorf("string[][] bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunArm64FreeOn(t, arrOfStrUnderflowSrc); code != 0 {
		t.Errorf("string[][] reclaim: code=%d", code)
	}
}

func TestWASMArrayOfStrReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, arrOfStrBumpSrc("50"))
	large := runWasm(t, arrOfStrBumpSrc("5000"))
	if small != large {
		t.Errorf("string[][] bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm two-word string[][] heap-allocates; expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, arrOfStrUnderflowSrc); got != 0 {
		t.Errorf("string[][] reclaim: got %d", got)
	}
}
