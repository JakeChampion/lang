package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Self-reassign of a string-BEARING struct local — `s = S{ a: s.a, b: ..., ... }`
// in a loop — now reclaims the orphaned struct in place. Strings are fully
// rc-tracked (docs/RC-STRINGS-PLAN.md: every slice DONE), so a string field
// shared into the new value via the functional copy is a COUNTED alias: the
// deep-drop of the old value dec's it (rc>=2 -> no free) rather than freeing a
// buffer the new value still points at. `typeSelfDropSafe` previously excluded
// any string-bearing type — a stale restriction predating string rc-tracking,
// now lifted.
//
// This pins the SAFETY contract: the shared string field stays intact across
// the churn (a wrong deep-drop would free it -> corruption / wrong compare) and
// no over-release (`__rc_underflow_count() == 0`). Memory is also bounded
// (verified manually: O(N) leak -> flat); here we gate correctness + safety,
// which a double-free would break.
const stringStructSelfReassignSrc = `struct S { a: string, b: string, n: i32 }
function step(s: S, i: i32): S { return S { a: s.a, b: s.b + "x", n: s.n + i }; }
function main(): i32 {
    var s: S = S { a: "shared", b: "", n: 0 };
    var i: i32 = 0;
    while (i < 300) { s = step(s, i); i = i + 1; }
    if (s.a != "shared") { return 90; }   // shared field survived the churn
    if (s.n != 44850) { return 91; }      // sum 0..299
    if (s.b.len() != 300) { return 92; }  // grown field correct
    return __rc_underflow_count();         // 0 == no double-free of a shared string
}`

func TestX86_64StringStructSelfReassign(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, stringStructSelfReassignSrc); code != 0 {
		t.Errorf("string-struct self-reassign: got %d, want 0", code)
	}
}

func TestArm64StringStructSelfReassign(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, stringStructSelfReassignSrc); code != 0 {
		t.Errorf("string-struct self-reassign: got %d, want 0", code)
	}
}

func TestWASMStringStructSelfReassign(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, stringStructSelfReassignSrc); got != 0 {
		t.Errorf("string-struct self-reassign: got %d, want 0", got)
	}
}
