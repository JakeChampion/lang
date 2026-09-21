package e2eselfhost

import (
	"path/filepath"
	"testing"
)

// A CAPTURE-FREE function value is static data (#9839).
//
// Every function value in the self-host is an environment box — `[body, caps…]`
// — so that a call through one dispatches the same way whatever it came from.
// When there are no captures the box is one word wide and that word is a code
// address, so every bit of it is known at compile time; the lowering built it
// with `const_func` + `arr_make` anyway, one heap block per evaluation, where
// native folds the same value to a static cell before emit
// (`InlineZeroCaptureClosures`). The visible half was E068: a `fip` body
// handing a bare lambda to a combinator was charged for an "array literal" the
// author never wrote, so a graded claim native accepted the self-host refused.
//
// The bound below is what makes the fix a fact rather than a claim about the
// emitted asm: `__heap_alloc_count()` may not move across a loop that builds
// one of these, on every backend. The capturing case is its other half — a
// fold applied to a lambda that DOES capture would read its environment out of
// a shared static block.
var captureFreeFnValueCases = []struct {
	name, src string
}{
	{"a-capture-free-value-costs-no-block", `function pick(): (i32) => i32 { return (x: i32): i32 => x * 2; }
function main(): i32 {
    var t: i32 = 0;
    var w: i32 = 0;
    while (w < 20) { var g: (i32) => i32 = pick(); t = t + g(1); w = w + 1; }
    var a1: i64 = __heap_alloc_count();
    var i: i32 = 0;
    while (i < 2000) { var g2: (i32) => i32 = pick(); t = t + g2(i); i = i + 1; }
    var a2: i64 = __heap_alloc_count();
    if (a2 != a1) { return 98; }
    if (t != 3998040) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`},
	// The same shape with one capture. It must still build a box per
	// evaluation, and each box must carry ITS round's capture — a value read
	// out of a shared block would answer with whichever round wrote last.
	{"a-capturing-value-still-builds-a-box", `function mk(n: i32): (i32) => i32 { return (x: i32): i32 => x + n; }
function main(): i32 {
    var t: i32 = 0;
    var w: i32 = 0;
    while (w < 20) { var g: (i32) => i32 = mk(w); t = t + g(1); w = w + 1; }
    var a1: i64 = __heap_alloc_count();
    var i: i32 = 0;
    while (i < 500) { var g2: (i32) => i32 = mk(i); t = t + g2(1); i = i + 1; }
    var a2: i64 = __heap_alloc_count();
    if (a2 - a1 < (500 as i64)) { return 98; }
    if (t != 125460) { return 97; }
    return 0;
}`},
	// Every position the lift wraps a capture-free value in: a struct field, an
	// array element, a returned lambda, a bare fn-name bound to a local, and a
	// lambda handed to a combinator. Each is a separate arm of the lift, and a
	// fold that got any one of their box layouts wrong reads a code address out
	// of the wrong word and jumps to it.
	{"every-position-dispatches", `import "std/array";
function dbl(x: i32): i32 { return x * 2; }
function inc(x: i32): i32 { return x + 1; }
struct Box { f: (i32) => i32 }
function apply(b: Box, v: i32): i32 { return b.f(v); }
function pick(which: i32): (i32) => i32 {
    if (which == 0) { return (x: i32): i32 => x * 3; }
    return (x: i32): i32 => x + 100;
}
function main(): i32 {
    var total: i32 = 0;
    var ys: i32[] = [1, 2, 3].map((x: i32): i32 => dbl(x));
    for y in ys { total = total + y; }
    if (total != 12) { return 91; }
    total = total + apply(Box { f: inc }, 5);
    if (total != 18) { return 92; }
    var fns: ((i32) => i32)[] = [dbl, inc];
    total = total + fns[0](10) + fns[1](10);
    if (total != 49) { return 93; }
    total = total + pick(0)(4) + pick(1)(4);
    if (total != 165) { return 94; }
    var h: (i32) => i32 = inc;
    total = total + h(0);
    if (total != 166) { return 95; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`},
}

func TestSelfHostCaptureFreeFnValue(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	for _, tc := range captureFreeFnValueCases {
		t.Run(tc.name, func(t *testing.T) {
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					exit, stderr := selfHostCLIRun(t, fernBin, stdlibRoot, tc.src, target)
					if exit != 0 {
						t.Fatalf("exit = %d, want 0 (9x = the assertion that failed)\n%s", exit, stderr)
					}
				})
			}
		})
	}
}
