package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Owned call-RESULT passed as a borrowed arg (statement-temp stage-(b)
// follow-up). `take(mk(i))` / `outer(inner(i))` — a fresh user-function
// result handed straight into another call — leaked the result every
// iteration (wasm: struct-result 800→80000, array-result 1600→160000). The
// original stage (b) stashed + dec'd only fresh-allocating literal SHAPES
// (freshOwnedRcTempType); this extends it to fresh-returning user-function
// calls (ownedCallResultType), reclaimed identically (stash + post-call dec
// via the is_unique-gated emitOwnedSlotDrop).
//
// Soundness is the union of the two existing guarantees: the ENCLOSING call
// is admitted only when its resolved result is a concrete scalar
// (resultCannotAliasArg), so it cannot hand the arg back; and the is_unique
// gate frees the arg only when it's uniquely owned (rc==1) — an aliased
// result (a callee returning its param, rc>=2 via the return-transfer inc)
// is merely dec'd, never freed.

func callResultArgStructBump(n string) string {
	return `struct P { x: i32, y: i32 }
function mk(v: i32): P { return P { x: v, y: v }; }
function take(p: P): i32 { return p.x + p.y; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + take(mk(i)); i = i + 1; }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func callResultArgArrBump(n string) string {
	return `function inner(v: i32): i32[] { return [v, v + 1, v + 2]; }
function outer(xs: i32[]): i32 { return xs[0] + xs[1] + xs[2]; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + outer(inner(i)); i = i + 1; }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// CRITICAL soundness: pass(arr) returns its param (aliased; rc>=2 via the
// return-transfer inc). It's a call-result arg to sum(); the post-call dec
// must only DEC it (is_unique false), never free, so `arr` stays valid and
// readable on the next line. sum(pass(arr)) == 60, + arr[0] == 10, ×200.
const callResultArgAliasedSafe = `function pass(p: i32[]): i32[] { return p; }
function sum(xs: i32[]): i32 { return xs[0] + xs[1] + xs[2]; }
function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    var arr: i32[] = [10, 20, 30];
    while (i < 200) {
        acc = acc + sum(pass(arr));
        acc = acc + arr[0];
        i = i + 1;
    }
    if (acc != 14000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64CallResultArgReclaim(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, callResultArgAliasedSafe); code != 0 {
		t.Errorf("aliased call-result-arg safety: code=%d (999=value mismatch, >0=over-release/UAF)", code)
	}
}

func TestArm64CallResultArgReclaim(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, callResultArgAliasedSafe); code != 0 {
		t.Errorf("aliased call-result-arg safety: code=%d", code)
	}
}

func TestWASMCallResultArgReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, c := range []struct {
		name string
		src  func(string) string
	}{
		{"struct-result", callResultArgStructBump},
		{"array-result", callResultArgArrBump},
	} {
		small := runWasm(t, c.src("50"))
		large := runWasm(t, c.src("5000"))
		if small != large {
			t.Errorf("%s call-result-arg bump should be bounded: N=50 -> %d, N=5000 -> %d", c.name, small, large)
		}
		if small == 0 {
			t.Errorf("%s: expected a non-zero bounded high-water, got 0", c.name)
		}
	}
	if got := runWasm(t, callResultArgAliasedSafe); got != 0 {
		t.Errorf("aliased call-result-arg safety: %d", got)
	}
}
