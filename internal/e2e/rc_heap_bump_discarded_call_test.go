package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Discarded owned call-result reclamation (statement-temp follow-up). A bare
// `mk(i);` whose user-function result is a fresh struct / array / string /
// enum dropped that allocation on the floor — nothing dec'd it, so a factory
// call in a loop leaked every result (wasm: struct 800→80000, array
// 1600→160000). Stage (a) excluded calls because a method call can alias its
// receiver; this slice reclaims the result of a USER function (in FuncSigs,
// not a `__`-prefixed builtin / mutator, not a variant constructor, not
// pair-form, not indirect) via the is_unique-gated emitOwnedTempStackDrop.
//
// Soundness: the dec only FREES a uniquely-owned (rc==1) result. A function
// that hands back an aliased value (its param / a field) carries the
// return-transfer inc, so the result is rc>=2 and the is_unique gate merely
// decs it — never frees a value the caller's source still owns. The excluded
// `__method_*` mutators (push / set) are the ones that return an UNCOUNTED
// rc==1 receiver alias the gate couldn't distinguish. Mirrors the shipped
// `var t = call(); /* t unused */` exit-sweep dec.

func discardedCallStructBump(n string) string {
	return `struct P { x: i32, y: i32 }
function mk(v: i32): P { return P { x: v, y: v }; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func discardedCallArrBump(n string) string {
	return `function mk(v: i32): i32[] { return [v, v + 1, v + 2]; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// CRITICAL soundness check: pass(arr) returns its param (aliased; the
// return-transfer inc makes the result rc>=2). Discarding it must only DEC
// (is_unique false), never free, so `arr` stays valid and readable. A wrong
// free shows up as a wrong sum (999) or a non-zero underflow count.
const discardedCallAliasedSafe = `function pass(p: i32[]): i32[] { return p; }
function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    var arr: i32[] = [10, 20, 30];
    while (i < 200) {
        pass(arr);
        acc = acc + arr[0] + arr[1] + arr[2];
        i = i + 1;
    }
    if (acc != 12000) { return 999; }
    return __rc_underflow_count();
}`

// Fresh result discarded, then a real one built + read: confirms the
// reclaim is value-neutral and over-release-free.
const discardedCallFreshSafe = `struct P { x: i32, y: i32 }
function mk(n: i32): P { return P { x: n, y: n + 1 }; }
function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        mk(i);
        var p: P = mk(i);
        acc = acc + p.x + p.y;
        i = i + 1;
    }
    // sum over i=0..199 of (i + (i+1)) = 2*19900 + 200 = 40000
    if (acc != 40000) { return 999; }
    return __rc_underflow_count();
}`

func checkDiscardedCallSafe(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	if _, code := run(t, discardedCallAliasedSafe); code != 0 {
		t.Errorf("aliased-return safety: code=%d (999=value mismatch, >0=over-release/UAF)", code)
	}
	if _, code := run(t, discardedCallFreshSafe); code != 0 {
		t.Errorf("fresh-result reclaim: code=%d", code)
	}
}

func TestX86_64DiscardedCallReclaim(t *testing.T) {
	checkDiscardedCallSafe(t, compileAndRunX86_64FreeOn)
}

func TestArm64DiscardedCallReclaim(t *testing.T) {
	checkDiscardedCallSafe(t, compileAndRunArm64FreeOn)
}

func TestWASMDiscardedCallReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, c := range []struct {
		name string
		src  func(string) string
	}{
		{"struct", discardedCallStructBump},
		{"array", discardedCallArrBump},
	} {
		small := runWasm(t, c.src("50"))
		large := runWasm(t, c.src("5000"))
		if small != large {
			t.Errorf("%s discarded-call bump should be bounded: N=50 -> %d, N=5000 -> %d", c.name, small, large)
		}
		if small == 0 {
			t.Errorf("%s: expected a non-zero bounded high-water, got 0", c.name)
		}
	}
	checkDiscardedCallSafe(t, func(t *testing.T, src string) (string, int) {
		return "", runWasm(t, src)
	})
}
