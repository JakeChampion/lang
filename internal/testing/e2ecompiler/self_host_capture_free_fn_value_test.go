package e2ecompiler

import (
	"path/filepath"
	"testing"
)

var captureFreeFnValueCases = []struct {
	name, src string
}{
	{"a-capture-free-value-costs-no-block", `function pick(): (i32) => i32 { return (x: i32): i32 => x * 2; }
function main(): i32 {
    let t: i32 = 0;
    let w: i32 = 0;
    while (w < 20) { let g: (i32) => i32 = pick(); t = t + g(1); w = w + 1; }
    let a1: i64 = __heap_alloc_count();
    let i: i32 = 0;
    while (i < 2000) { let g2: (i32) => i32 = pick(); t = t + g2(i); i = i + 1; }
    let a2: i64 = __heap_alloc_count();
    if (a2 != a1) { return 98; }
    if (t != 3998040) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`},
	{"a-capturing-value-still-builds-a-box", `function mk(n: i32): (i32) => i32 { return (x: i32): i32 => x + n; }
function main(): i32 {
    let t: i32 = 0;
    let w: i32 = 0;
    while (w < 20) { let g: (i32) => i32 = mk(w); t = t + g(1); w = w + 1; }
    let a1: i64 = __heap_alloc_count();
    let i: i32 = 0;
    while (i < 500) { let g2: (i32) => i32 = mk(i); t = t + g2(1); i = i + 1; }
    let a2: i64 = __heap_alloc_count();
    if (a2 - a1 < (500 as i64)) { return 98; }
    if (t != 125460) { return 97; }
    return 0;
}`},
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
    let total: i32 = 0;
    let ys: i32[] = [1, 2, 3].map((x: i32): i32 => dbl(x));
    for y in ys { total = total + y; }
    if (total != 12) { return 91; }
    total = total + apply(Box { f: inc }, 5);
    if (total != 18) { return 92; }
    let fns: ((i32) => i32)[] = [dbl, inc];
    total = total + fns[0](10) + fns[1](10);
    if (total != 49) { return 93; }
    total = total + pick(0)(4) + pick(1)(4);
    if (total != 165) { return 94; }
    let h: (i32) => i32 = inc;
    total = total + h(0);
    if (total != 166) { return 95; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`},
	{"fn-names-without-a-lambda", `function dbl(x: i32): i32 { return x * 2; }
function inc(x: i32): i32 { return x + 1; }
struct Box { f: (i32) => i32 }
function apply(b: Box, v: i32): i32 { return b.f(v); }
function twice(f: (i32) => i32, v: i32): i32 { return f(f(v)); }
function main(): i32 {
    let total: i32 = apply(Box { f: inc }, 5);
    if (total != 6) { return 91; }
    let fns: ((i32) => i32)[] = [dbl, inc];
    total = total + fns[0](10) + fns[1](10);
    if (total != 37) { return 92; }
    let h: (i32) => i32 = dbl;
    total = total + h(1) + twice(inc, 0) + dbl(1);
    if (total != 43) { return 93; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`},
}

func TestSelfHostCaptureFreeFnValue(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
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
