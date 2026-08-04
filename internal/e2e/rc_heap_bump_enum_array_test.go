package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Array-of-enum inner-payload reclamation (RC-Perceus Phase 6). An `E[]`
// whose enum carries rc-tracked payloads (string / array / struct / nested
// enum) kept the flat `__fern_drop_arr_ptr` — it freed the outer buffer and
// flat-rc_dec'd each enum box, but never traversed the box, so every payload
// leaked (wasm probe: 3264 B → 320064 B, linear). The fix routes an
// enum-element array through the generic `__drop_arr_of_<__drop_enum_E>`
// loop (genArrOfArrDropFn calls the enum's own is_unique-gated deep drop per
// element, then frees the outer buffer) — the same recursion the merged
// array-of-(struct[]/array[]) slice uses. Generic instantiations
// (`Option[string][]`) register their substituted decl so the worklist
// regenerates `__drop_enum_<mangled>`; nested `E[][]` recurses through a
// second `__drop_arr_of_` wrapper.
//
// Proof surface: wasm two-word strings always heap-allocate, so its bump
// high-water is the bounded-growth gate (flat with the deep drop, linear
// without). All three backends run an IN-PROGRAM value + over-release check:
// the program reads every heap payload back, sums their lengths, compares to
// the expected total IN FERN (so the verdict survives the 8-bit native exit
// code), and returns __rc_underflow_count() — 0 iff value-correct AND no
// payload was double-dropped. A premature free / corrupted payload shows up
// as the 99 value-mismatch sentinel; an over-release shows up as a non-zero
// underflow count.

func enumArrBumpSrc(n string) string {
	return `enum Box { Val(string), Empty }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        var xs: Box[] = [Val("hello there friend, "), Val("general kenobi!!!"), Empty];
        acc = acc + xs.len();
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Concrete Box[] — reads both heap-string payloads back: 20 + 17 == 37, ×200
// == 7400.
const enumArrCheckBox = `enum Box { Val(string), Empty }
function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        var xs: Box[] = [Val("hello there friend, "), Val("general kenobi!!!"), Empty];
        var a: i32 = match (xs[0]) { Val(s) => s.len(), Empty => 0 };
        var b: i32 = match (xs[1]) { Val(s) => s.len(), Empty => 0 };
        acc = acc + a + b;
        i = i + 1;
    }
    if (acc != 7400) { return 99; }
    return __rc_underflow_count();
}`

// Generic Option[string][] — exercises the substituted-decl registration:
// 18 + 6 == 24, ×200 == 4800.
const enumArrCheckOption = `function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        var xs: Option[string][] = [Some("hello there friend"), None, Some("kenobi")];
        var a: i32 = match (xs[0]) { Some(s) => s.len(), None => 0 };
        var b: i32 = match (xs[2]) { Some(s) => s.len(), None => 0 };
        acc = acc + a + b;
        i = i + 1;
    }
    if (acc != 4800) { return 99; }
    return __rc_underflow_count();
}`

// Nested Box[][] — exercises the recursive __drop_arr_of_ wrapper:
// 5 + 4 + 5 == 14, ×200 == 2800.
const enumArrCheckNested = `enum Box { Val(string), Empty }
function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        var g: Box[][] = [[Val("alpha"), Empty], [Val("beta"), Val("gamma")]];
        var a: i32 = match (g[0][0]) { Val(s) => s.len(), Empty => 0 };
        var b: i32 = match (g[1][0]) { Val(s) => s.len(), Empty => 0 };
        var c: i32 = match (g[1][1]) { Val(s) => s.len(), Empty => 0 };
        acc = acc + a + b + c;
        i = i + 1;
    }
    if (acc != 2800) { return 99; }
    return __rc_underflow_count();
}`

func checkEnumArr(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	for _, c := range []struct{ name, src string }{
		{"box", enumArrCheckBox},
		{"option", enumArrCheckOption},
		{"nested", enumArrCheckNested},
	} {
		if _, code := run(t, c.src); code != 0 {
			t.Errorf("%s: code=%d (99=value mismatch, >0=over-release)", c.name, code)
		}
	}
}

func TestX86_64EnumArrayReclaim(t *testing.T) {
	checkEnumArr(t, compileAndRunX86_64FreeOn)
}

func TestArm64EnumArrayReclaim(t *testing.T) {
	checkEnumArr(t, compileAndRunArm64FreeOn)
}

func TestWASMEnumArrayReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, enumArrBumpSrc("50"))
	large := runWasm(t, enumArrBumpSrc("5000"))
	if small != large {
		t.Errorf("enum-array bump should be bounded (payload reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, enumArrCheckBox); got != 0 {
		t.Errorf("box: %d (99=value mismatch, >0=over-release)", got)
	}
	if got := runWasm(t, enumArrCheckOption); got != 0 {
		t.Errorf("option: %d", got)
	}
	if got := runWasm(t, enumArrCheckNested); got != 0 {
		t.Errorf("nested: %d", got)
	}
}
