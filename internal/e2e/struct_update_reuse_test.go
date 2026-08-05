// Runtime contract for the struct-update spread `p = T{ ...p, f: v }` now
// that it reuses p's box in place (internal/ir/struct_update_reuse_test.go
// pins the lowering). Reuse is gated on a runtime is_unique check, so the
// two branches are BOTH live and each needs its own proof:
//
//   - unique p (rc==1): the box is repurposed. The un-listed fields are
//     already in it and are left alone.
//   - ALIASED p (rc>1): a fresh box is allocated and the alias must keep the
//     old value intact — which means the un-listed fields have to be COPIED
//     into the new box. Those fields are exactly what the old lowering
//     refused this shape over ("the fresh-alloc branch's un-listed fields
//     are left uninitialised, read back as 0"), so an alias reading 0 out of
//     a field nobody listed is the specific regression these cases catch.
package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Both branches in one program, so a single exit code covers them.
//
// `keep` aliases `s` before the update, forcing the fresh-alloc branch: it
// must still read m=7 and its 3-element xs afterwards. `s` then takes the
// unique branch on the next update (the alias is gone by then only if the
// compiler says so — either way the value must be right). The exit code sums
// values that are each wrong in a different way if a field is dropped:
//
//	keep.m (7) + keep.xs.len() (3) + s.m (7) + s.xs.len() (3) + s.n (2) = 22
const structUpdateSpreadSrc = `struct S { xs: i32[], n: i32, m: i32 }
function main(): i32 {
    var s: S = S { xs: [1, 2, 3], n: 0, m: 7 };
    var keep: S = s;
    s = S { ...s, n: 1 };
    s = S { ...s, n: s.n + 1 };
    if (keep.n != 0) { return 253; }
    return keep.m + keep.xs.len() + s.m + s.xs.len() + s.n;
}`

// The record-update idiom must not cross the append cliff: threading an
// accumulator through `p = T{ ...p, xs: p.xs.append(v) }` is how a Fern
// program builds an array inside a struct, and every crossing there copies
// the whole buffer. `own` on the parameter is what makes the callee's box
// uniquely owned; without it the caller's borrow keeps the array at rc>1 and
// each append copies. 20 appends over 10 calls, so a regression reads as 20,
// not as a rounding difference.
const structUpdateSpreadCliffSrc = `struct S { xs: i32[], n: i32 }
function push(own s: S, v: i32): S {
    s = S { ...s, xs: s.xs.append(v) };
    s = S { ...s, xs: s.xs.append(v + 1) };
    return s;
}
function main(): i32 {
    var s: S = S { xs: [], n: 0 };
    var i: i32 = 0;
    while (i < 10) { s = push(s, i); i = i + 1; }
    if (s.xs.len() != 20) { return 254; }
    return __arr_push_shared_count();
}`

func checkStructUpdateSpread(t *testing.T, backend string, gotValue, gotCliff int) {
	t.Helper()
	if gotValue == 253 {
		t.Fatalf("%s: the alias saw the update — reuse fired on a box with rc>1, which is a "+
			"write into another live value, not a missing field", backend)
	}
	if gotValue != 22 {
		t.Errorf("%s: struct-update spread = %d, want 22 — an alias reading 0 from a "+
			"field the literal never listed means the fresh-alloc branch dropped it",
			backend, gotValue)
	}
	if gotCliff == 254 {
		t.Fatalf("%s: the accumulator built the WRONG array; the cliff reading below it is meaningless", backend)
	}
	if gotCliff != 0 {
		t.Errorf("%s: record-update accumulator crossed the append cliff %d times, want 0 — "+
			"each crossing copies the whole buffer, so this is the quadratic shape", backend, gotCliff)
	}
}

func TestX86_64StructUpdateSpreadReuse(t *testing.T) {
	_, value := compileAndRunX86_64FreeOn(t, structUpdateSpreadSrc)
	_, cliff := compileAndRunX86_64FreeOn(t, structUpdateSpreadCliffSrc)
	checkStructUpdateSpread(t, "x86-64", value, cliff)
}

func TestArm64StructUpdateSpreadReuse(t *testing.T) {
	_, value := compileAndRunArm64(t, structUpdateSpreadSrc)
	_, cliff := compileAndRunArm64(t, structUpdateSpreadCliffSrc)
	checkStructUpdateSpread(t, "arm64", value, cliff)
}

func TestWASMStructUpdateSpreadReuse(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	checkStructUpdateSpread(t, "wasm", runWasm(t, structUpdateSpreadSrc), runWasm(t, structUpdateSpreadCliffSrc))
}

// The interpreter has no refcounts, so it reuses nothing and crosses nothing —
// it is the oracle for the VALUE half and a constant 0 for the cliff half.
func TestInterpStructUpdateSpreadReuse(t *testing.T) {
	checkStructUpdateSpread(t, "interp",
		runInterpExit(t, structUpdateSpreadSrc), runInterpExit(t, structUpdateSpreadCliffSrc))
}
