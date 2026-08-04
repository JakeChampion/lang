package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Field-of-fresh reclamation (the FieldAccess sibling of index-of-fresh /
// `.len()`). A scalar field access on a fresh struct/tuple result —
// `mk(i).x` — loaded the field and dropped the box on the floor, leaking it
// every iteration (struct 240000 -> 2400000 in a loop). The FieldAccess
// lowering only saw declared-var targets; a fresh call / literal container
// was orphaned.
//
// Fix: stash a fresh owned struct/tuple container (freshOwnedRcTempType — a
// struct/tuple literal — or ownedCallResultType — a fresh-returning call),
// load the field off the reload, then deep-drop the container via the
// is_unique-gated emitOwnedSlotDrop (which also reclaims the container's OTHER
// rc fields — we only extracted a scalar). Gated to a NON-POINTER field: the
// loaded scalar can't alias the box; a pointer field (`mk().data` -> array)
// WOULD alias and is left alone (safe leak). The is_unique gate protects an
// aliased container (a callee returning its param, rc>=2 — only dec'd).

func fieldOfFreshStructBump(n string) string {
	return `struct P { x: i32, data: i32[] }
function mk(v: i32): P { return P { x: v, data: [v, v + 1, v + 2] }; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { var v: i32 = mk(i).x; acc = acc + v; i = i + 1; }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func fieldOfFreshTupleBump(n string) string {
	return `function mk(v: i32): (i32, i32[]) { return (v, [v, v + 1]); }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { var v: i32 = mk(i).0; acc = acc + v; i = i + 1; }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// CRITICAL soundness: pass(s) returns its param (aliased; rc>=2 via the
// return-transfer inc). Field-accessing it must only DEC (is_unique false),
// never free, so `s` stays valid. pass(s).x==10, +s.y==20, ==30 per iter,
// x200 == 6000.
const fieldOfFreshAliasedSafe = `struct P { x: i32, y: i32 }
function pass(p: P): P { return p; }
function main(): i32 {
    var s: P = P { x: 10, y: 20 };
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) { var v: i32 = pass(s).x; acc = acc + v + s.y; i = i + 1; }
    if (acc != 6000) { return 99; }
    return __rc_underflow_count();
}`

// Pointer-field container must NOT be reclaimed (the loaded array aliases the
// box's field). Verifies value-correctness + 0-over-release on the safe-leak
// path. mk(i).data[0]==i, sum i=0..199 == 19900.
const fieldOfFreshPtrField = `struct P { x: i32, data: i32[] }
function mk(v: i32): P { return P { x: v, data: [v, v + 1, v + 2] }; }
function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) { var d: i32[] = mk(i).data; acc = acc + d[0]; i = i + 1; }
    if (acc != 19900) { return 99; }
    return __rc_underflow_count();
}`

func checkFieldOfFresh(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	if _, code := run(t, fieldOfFreshAliasedSafe); code != 0 {
		t.Errorf("aliased-container safety: code=%d (99=value, >0=over-release/UAF)", code)
	}
	if _, code := run(t, fieldOfFreshPtrField); code != 0 {
		t.Errorf("pointer-field value/over-release: code=%d", code)
	}
}

func TestX86_64FieldOfFreshReclaim(t *testing.T) {
	checkFieldOfFresh(t, compileAndRunX86_64FreeOn)
}

func TestArm64FieldOfFreshReclaim(t *testing.T) {
	checkFieldOfFresh(t, compileAndRunArm64FreeOn)
}

func TestWASMFieldOfFreshReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, c := range []struct {
		name string
		src  func(string) string
	}{
		{"struct", fieldOfFreshStructBump},
		{"tuple", fieldOfFreshTupleBump},
	} {
		small := runWasm(t, c.src("5000"))
		large := runWasm(t, c.src("50000"))
		if small != large {
			t.Errorf("%s field-of-fresh bump should be bounded: N=5000 -> %d, N=50000 -> %d", c.name, small, large)
		}
		if small == 0 {
			t.Errorf("%s: expected a non-zero bounded high-water, got 0", c.name)
		}
	}
	checkFieldOfFresh(t, func(t *testing.T, src string) (string, int) {
		return "", runWasm(t, src)
	})
}
