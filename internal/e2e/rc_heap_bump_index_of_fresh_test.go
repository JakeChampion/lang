package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Index-of-fresh-array reclamation (stage-(c) value-consuming-op sibling). A
// scalar index into a fresh array result — `mk(i)[1]` — loaded the element and
// dropped the buffer on the floor, leaking it every iteration (160000 ->
// 1600000 in a loop). The Index lowering now stashes a fresh owned array
// container (freshOwnedRcTempType — an array literal — or ownedCallResultType
// — a fresh-returning call), indexes off the reload, then dec's it via the
// is_unique-gated emitOwnedSlotDrop.
//
// Gated to a NON-POINTER element: the loaded scalar can't alias the buffer, so
// freeing it is safe; a pointer element (`mks()[0]` -> string) WOULD alias the
// buffer and is deliberately left alone (still leaks, but never a UAF). The
// is_unique gate additionally protects an aliased container (a callee
// returning its param, rc>=2 via the return-transfer inc — only dec'd).

func indexFreshBumpSrc(n string) string {
	return `function mk(v: i32): i32[] { return [v, v + 1, v + 2]; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { var v: i32 = mk(i)[1]; acc = acc + v; i = i + 1; }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// CRITICAL soundness: pass(arr) returns its param (aliased; rc>=2 via the
// return-transfer inc). Indexing it must only DEC (is_unique false), never
// free, so `arr` stays valid. mk[1]==arr[1]==20, +arr[0]==10, ==30 per iter,
// x200 == 6000.
const indexFreshAliasedSafe = `function pass(p: i32[]): i32[] { return p; }
function main(): i32 {
    var arr: i32[] = [10, 20, 30];
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) { var v: i32 = pass(arr)[1]; acc = acc + v + arr[0]; i = i + 1; }
    if (acc != 6000) { return 99; }
    return __rc_underflow_count();
}`

// Pointer-element container must NOT be reclaimed (the loaded string aliases
// the buffer). Verifies value-correctness + 0-over-release on the safe-leak
// path. "alpha"==5, x200 == 1000.
const indexFreshPtrElem = `function mks(): string[] { return ["alpha", "beta", "gamma"]; }
function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) { var s: string = mks()[0]; acc = acc + s.len(); i = i + 1; }
    if (acc != 1000) { return 99; }
    return __rc_underflow_count();
}`

func checkIndexFresh(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	if _, code := run(t, indexFreshAliasedSafe); code != 0 {
		t.Errorf("aliased-container safety: code=%d (99=value, >0=over-release/UAF)", code)
	}
	if _, code := run(t, indexFreshPtrElem); code != 0 {
		t.Errorf("pointer-element value/over-release: code=%d", code)
	}
}

func TestX86_64IndexOfFreshReclaim(t *testing.T) {
	checkIndexFresh(t, compileAndRunX86_64FreeOn)
}

func TestArm64IndexOfFreshReclaim(t *testing.T) {
	checkIndexFresh(t, compileAndRunArm64FreeOn)
}

func TestWASMIndexOfFreshReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, indexFreshBumpSrc("5000"))
	large := runWasm(t, indexFreshBumpSrc("50000"))
	if small != large {
		t.Errorf("index-of-fresh bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	checkIndexFresh(t, func(t *testing.T, src string) (string, int) {
		return "", runWasm(t, src)
	})
}
