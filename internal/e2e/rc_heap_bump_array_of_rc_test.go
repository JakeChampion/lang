package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Array-of-(rc-inner-array) reclamation (RC-Perceus). The string[][]
// slice reclaimed string inner arrays; this generalises the array-of-
// array recursion to ANY rc-tracked inner element — struct[][] (`P[][]`),
// array-of-array-of-array (`i32[][][]`), etc. Before, these kept the flat
// __fern_drop_arr_ptr (outer buffer freed, inner buffers leaked):
// `P[][]` grew 4864 B → 480064 B, `i32[][][]` 6464 B → 640064 B. Now
// arrElemStructDropName routes an rc-inner-array through a generated
// __drop_arr_of_<perElem> loop whose per-element call is the INNER
// array's own deep drop (__drop_arr_struct_<E> for P[][], __drop_arr_arr_n
// for i32[][][]), recursively — each is_unique-gated, so shared inner
// arrays only dec.

func arrOfStructBumpSrc(n string) string {
	return `struct P { x: i32, y: i32 }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        var g: P[][] = [[P{x: i, y: 1}], [P{x: 2, y: i}]];
        acc = acc + g[0][0].x;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func arrOf3DBumpSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        var g: i32[][][] = [[[i, i + 1]], [[i + 2]]];
        acc = acc + g[0][0][1];
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Both shapes value-correct + no over-release across many iterations.
const arrOfRcUnderflowSrc = `struct P { x: i32, y: i32 }
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var g: P[][] = [[P{x: i, y: 1}, P{x: 7, y: 2}], [P{x: 2, y: i}]];
        var h: i32[][][] = [[[i, i + 1]], [[i + 2, i + 3]]];
        acc = acc + g[0][1].x + g[1][0].y + h[0][0][1] + h[1][0][0];
        i = i + 1;
    }
    // per iter: 7 + i + (i+1) + (i+2) = 3i+10; sum i=0..199 = 3*19900 + 2000 = 61700
    if (acc != 61700) { return 999; }
    return __rc_underflow_count();
}`

// runArrOfRc asserts bounded reclamation (N=50 high-water == N=5000) for
// both shapes. `requireNonZero` is set only for wasm — its two-word /
// no-SSO heap layout always allocates, so the bump plateaus at a non-zero
// value; natives can reclaim to a flat 0 high-water (perfect freelist
// reuse), which is equally bounded.
func runArrOfRc(t *testing.T, run func(*testing.T, string) int, requireNonZero bool) {
	for name, src := range map[string]func(string) string{"struct[][]": arrOfStructBumpSrc, "i32[][][]": arrOf3DBumpSrc} {
		small := run(t, src("50"))
		large := run(t, src("5000"))
		if small != large {
			t.Errorf("%s bump should be bounded (inner reclaim): N=50 -> %d, N=5000 -> %d", name, small, large)
		}
		if requireNonZero && small == 0 {
			t.Errorf("%s: wasm heap-allocates; expected a non-zero bounded high-water, got 0", name)
		}
	}
}

func TestX86_64ArrayOfRcReclaim(t *testing.T) {
	runArrOfRc(t, mustRunX86_64FreeOn, false)
	if _, code := compileAndRunX86_64FreeOn(t, arrOfRcUnderflowSrc); code != 0 {
		t.Errorf("array-of-rc reclaim: code=%d (999=value mismatch, >0=over-release)", code)
	}
}

func TestArm64ArrayOfRcReclaim(t *testing.T) {
	runArrOfRc(t, mustRunArm64FreeOn, false)
	if _, code := compileAndRunArm64FreeOn(t, arrOfRcUnderflowSrc); code != 0 {
		t.Errorf("array-of-rc reclaim: code=%d", code)
	}
}

func TestWASMArrayOfRcReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	runArrOfRc(t, runWasm, true)
	if got := runWasm(t, arrOfRcUnderflowSrc); got != 0 {
		t.Errorf("array-of-rc reclaim: got %d", got)
	}
}
