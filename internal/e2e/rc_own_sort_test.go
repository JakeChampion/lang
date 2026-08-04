package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Dogfood: std/sort's `own`-taking in-place sorts. `sort_i32_inplace_asc/_desc`
// consume their array argument and return the SAME buffer, sorted — no copy,
// zero allocation. These exercise the `own` feature (affine + ownership-guard +
// transfer-by-return) through real stdlib code: a fresh literal is an owned
// value (passes the E051 guard), the function permutes it in place, and the
// returned array IS the input box (the own param escapes via `return`, so the
// exit sweep must not free it).

const ownInplaceSortSrc = `
import "std/sort";
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var a: i32[] = sort.sort_i32_inplace_asc([5, 3, 8, 1, 9, 2, 7, 4, 6]);
        if (a[0] != 1) { return 10; }
        if (a[8] != 9) { return 11; }
        if (a[4] != 5) { return 12; }   // median
        var b: i32[] = sort.sort_i32_inplace_desc([5, 3, 8, 1, 9, 2, 7, 4, 6]);
        if (b[0] != 9) { return 20; }
        if (b[8] != 1) { return 21; }
        acc = acc + a[0] + b[0];        // 1 + 9 = 10 per iter
        i = i + 1;
    }
    if (acc != 1000) { return 1; }
    return __rc_underflow_count();
}`

func TestX86_64OwnInplaceSort(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, ownInplaceSortSrc); code != 0 {
		t.Errorf("in-place own sort: got %d, want 0", code)
	}
}

func TestArm64OwnInplaceSort(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, ownInplaceSortSrc); code != 0 {
		t.Errorf("in-place own sort: got %d, want 0", code)
	}
}

func TestWASMOwnInplaceSort(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, ownInplaceSortSrc); got != 0 {
		t.Errorf("in-place own sort: got %d, want 0", got)
	}
	// Bounded: the in-place sort doesn't allocate an output buffer (it returns
	// the input box), so a sort loop holds a flat high-water rather than leaking
	// a fresh sorted array per iteration.
	bump := func(n string) string {
		return `
import "std/sort";
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) {
        var a: i32[] = sort.sort_i32_inplace_asc([5, 3, 8, 1, 9, 2, 7, 4, 6]);
        var u: i32 = a[0];
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
	}
	if small, large := runWasm(t, bump("500")), runWasm(t, bump("5000")); small != large {
		t.Errorf("in-place sort should be bounded: N=500 -> %d, N=5000 -> %d", small, large)
	}
}
