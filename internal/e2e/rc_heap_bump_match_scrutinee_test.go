package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Match-on-fresh-enum-scrutinee reclamation (value-consuming-position sibling
// of index-of-fresh / .len()-of-fresh; docs/RC-PERCEUS-PLAN.md). A match
// consumes its scrutinee — the arms read payload fields out of the box, then
// it's dead — but the box was never dec'd, so `match (mk(i)) { … }` over a
// per-iteration-fresh `mk(i)` leaked one box every iteration.
//
// The Match / MatchExpr lowering now reclaims a FRESH owned enum scrutinee
// (ownedCallResultType) once the match completes. The drop routes through the
// generated `__drop_enum_<Name>` fn (emitEnumDropViaGenFn → is_unique-gated
// variant-plan + exact-size box_free) — the wasm-correct path: a box_free
// inside a generated drop FUNCTION returns the box to the freelist on every
// backend, where the same box_free emitted INLINE in the match body does not
// reuse on wasm. Gated on every arm binding being non-pointer (and, for the
// expression form, the result too), so no payload escapes the freed box.
//
// The enum here is THREE-variant (A(i32) / B(i32) / C) so it's genuinely
// heap-boxed: a two-variant `Val(i32) | Empty` enum is pair-form (Option[i32]
// shape), returned as an unboxed (tag, payload) pair the callee never heap-
// allocates — ownedCallResultType excludes pair-form callees, so this feature
// correctly leaves them to the pair-form machinery.

// Expression-form scrutinee: `var r = match (mk(i)) { A(x) => x, … }`.
func matchScrutineeExprBumpSrc(n string) string {
	return `enum E3 { A(i32), B(i32), C }
function mk(v: i32): E3 { return A(v); }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        var r: i32 = match (mk(i)) { A(x) => x, B(y) => y, C => 0 };
        acc = acc + r;
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Statement-form scrutinee: `match (mk(i)) { A(x) => {…}, … }`.
func matchScrutineeStmtBumpSrc(n string) string {
	return `enum E3 { A(i32), B(i32), C }
function mk(v: i32): E3 { return A(v); }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        match (mk(i)) {
            A(x) => { acc = acc + x; },
            B(y) => { acc = acc + y; },
            C => {},
        }
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// CRITICAL soundness: pass(b) returns its param (aliased; rc>=2 via the
// return-transfer inc), so matching pass(b) must only DEC (is_unique false),
// never free — `b` stays a valid box, re-read after the loop. acc = 7*200 =
// 1400; final read of b must still be 7. Returns __rc_underflow_count() (0 iff
// value-correct AND no over-release / UAF).
const matchScrutineeAliasedSafe = `enum E3 { A(i32), B(i32), C }
function pass(b: E3): E3 { return b; }
function main(): i32 {
    var b: E3 = A(7);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var r: i32 = match (pass(b)) { A(x) => x, B(y) => y, C => 0 };
        acc = acc + r;
        i = i + 1;
    }
    var final: i32 = match (b) { A(x) => x, B(y) => y, C => 0 };
    if (acc != 1400) { return 99; }
    if (final != 7) { return 88; }
    return __rc_underflow_count();
}`

func checkMatchScrutineeSafe(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	if _, code := run(t, matchScrutineeAliasedSafe); code != 0 {
		t.Errorf("aliased-scrutinee safety: code=%d (99=value, 88=UAF/freed-early, >0=over-release)", code)
	}
}

func TestX86_64MatchScrutineeReclaim(t *testing.T) {
	for _, mk := range []func(string) string{matchScrutineeExprBumpSrc, matchScrutineeStmtBumpSrc} {
		small := mustRunX86_64FreeOn(t, mk("50"))
		large := mustRunX86_64FreeOn(t, mk("5000"))
		if small != large {
			t.Errorf("match-scrutinee bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
		}
	}
	checkMatchScrutineeSafe(t, compileAndRunX86_64FreeOn)
}

func TestArm64MatchScrutineeReclaim(t *testing.T) {
	for _, mk := range []func(string) string{matchScrutineeExprBumpSrc, matchScrutineeStmtBumpSrc} {
		small := mustRunArm64FreeOn(t, mk("50"))
		large := mustRunArm64FreeOn(t, mk("5000"))
		if small != large {
			t.Errorf("match-scrutinee bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
		}
	}
	checkMatchScrutineeSafe(t, compileAndRunArm64FreeOn)
}

func TestWASMMatchScrutineeReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, mk := range []func(string) string{matchScrutineeExprBumpSrc, matchScrutineeStmtBumpSrc} {
		small := runWasm(t, mk("50"))
		large := runWasm(t, mk("5000"))
		if small != large {
			t.Errorf("match-scrutinee bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
		}
		if small == 0 {
			t.Errorf("wasm heap-allocates; expected a non-zero bounded high-water, got 0")
		}
	}
	checkMatchScrutineeSafe(t, func(t *testing.T, src string) (string, int) {
		return "", runWasm(t, src)
	})
}
