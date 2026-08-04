package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// `?`-consumed source-box reclamation (the try-operator sibling of the
// match-scrutinee reclaim; #4355). `mk(pre)?` evaluates the callee's
// Option/Result box into a scratch slot, reads the success payload, and the
// box is dead — but it was never dec'd, so a per-iteration `?` leaked one box
// per success. Two shapes, two mechanisms:
//
//   - HEAP-FORM inner (a pointer payload forces a real box): gated by
//     reclaimableTryScrutinee (ownedCallResultType + EnumRcPayloads-eligible +
//     scalar-or-string payload) and freed by emitTryBoxFree — is_unique-gated
//     shallow box_free with the SUCCESS variant's exact size (tag==0 proven on
//     the path). A STRING payload's reference MOVES to the extracted value,
//     and rhsTainted's TryOp case credits the binding as owned so the exit
//     sweep balances it.
//   - PAIR-FORM inner (scalar payloads — Option[i32] / Result[i32, i32]): the
//     TryOp site does NOT suppress the pair rebox, so emitRepackPairAsHeapBox
//     allocates a fresh rc=1 box per evaluation; tryPairReboxSize +
//     emitTryBoxFreeSized free it with the repack's exact size.
//
// Safety rides the is_unique gate: an aliased box (a callee returning its
// param carries the return-transfer inc, rc>=2) is only dec'd, never freed —
// pinned by the passthrough cases below (underflow count must stay 0 and the
// source values must remain readable).

// Scalar Result payload through `?` — pair-form inner, rebox freed.
func tryScrutScalarBumpSrc(n string) string {
	return `function mk(pre: string): Result[i32, i32] { return Ok(pre.len()); }
function go(pre: string): Result[i32, i32] { var v: i32 = mk(pre)?; return Ok(v + 1); }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        match (go("ab")) { Ok(v) => { acc = acc + v; }, Err(e) => { acc = acc + e; }, }
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Scalar Option payload through `?` with a real failure path (fresh None
// propagation) — both edges must stay bounded.
func tryScrutOptionBumpSrc(n string) string {
	return `function mko(pre: string, k: i32): Option[i32] { if (k > 0) { return None; } return Some(pre.len()); }
function go(pre: string, k: i32): Option[i32] { var v: i32 = mko(pre, k)?; return Some(v + 1); }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        match (go("ab", i % 2)) { Some(v) => { acc = acc + v; }, None => { acc = acc + 1; }, }
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// STRING success payload through `?` — on the natives (x86-64 / arm64) a
// pointer payload forces the callee HEAP-form: the box is freed shallow at
// the consume edge and the payload's reference MOVES to the `var s: string`
// binding (rhsTainted TryOp case → owned → exit-sweep dec). The Err leg
// (pair-form enclosing) copies (tag, payload) out and frees the source box
// with the Err variant's size.
//
// On wasm32 a pointer payload is pair-form-shaped, so the callee is
// PAIR-form: the rebox is freed (bounded boxes — see the literal-payload
// case below) but the pair-form constructor return carries NO alias-inc, so
// the extracted CONCAT payload cannot soundly take ownership and keeps a
// documented leak (#4355's construction-side alias-inc discipline follow-up).
// The wasm leg therefore asserts the literal-payload shape for boundedness
// and pins this shape as correctness + detector only.
func tryScrutStringBumpSrc(n string) string {
	return `function mk(pre: string, k: i32): Result[string, i32] { if (k > 2) { return Err(7); } return Ok(pre + "abc"); }
function go(pre: string, k: i32): Result[i32, i32] { var s: string = mk(pre, k)?; return Ok(s.len()); }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        match (go("ab", i % 4)) { Ok(v) => { acc = acc + v; }, Err(e) => { acc = acc + e; }, }
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// STRING payload with a STATIC literal (`Ok("abcde")`) — bounded on every
// backend: the box (heap-form on natives, pair-rebox on wasm) is freed at
// both consume edges, and the literal payload is .rodata (nothing to free).
func tryScrutStrLitBumpSrc(n string) string {
	return `function mk(pre: string, k: i32): Result[string, i32] { if (k > 2) { return Err(7); } return Ok("abcde"); }
function go(pre: string, k: i32): Result[i32, i32] { var s: string = mk(pre, k)?; return Ok(s.len()); }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        match (go("ab", i % 4)) { Ok(v) => { acc = acc + v; }, Err(e) => { acc = acc + e; }, }
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Wasm-only correctness pin for the concat-payload shape (see the
// tryScrutStringBumpSrc doc): values stay right and nothing over-releases;
// the concat payload itself keeps the documented pair-form leak.
const tryScrutStringWasmSound = `function mk(pre: string, k: i32): Result[string, i32] { if (k > 2) { return Err(7); } return Ok(pre + "abc"); }
function go(pre: string, k: i32): Result[i32, i32] { var s: string = mk(pre, k)?; return Ok(s.len()); }
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 2000) {
        match (go("ab", i % 4)) { Ok(v) => { acc = acc + v; }, Err(e) => { acc = acc + e; }, }
        i = i + 1;
    }
    if (acc != 11000) { return 99; }
    return __rc_underflow_count();
}`

// CRITICAL soundness 1: id2(r) returns its param (aliased; rc>=2 via the
// return-transfer inc), so the `?`-edge free must only DEC (is_unique false).
// The extracted string must stay valid and nothing may over-release.
const tryScrutAliasedBoxSafe = `function mk(pre: string): Result[string, i32] { return Ok(pre + "abc"); }
function id2(r: Result[string, i32]): Result[string, i32] { return r; }
function go(pre: string): Result[i32, i32] { var s: string = id2(mk(pre))?; return Ok(s.len()); }
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        match (go("ab")) { Ok(v) => { acc = acc + v; }, Err(e) => { acc = acc + e; }, }
        i = i + 1;
    }
    if (acc != 1000) { return 99; }
    return __rc_underflow_count();
}`

// CRITICAL soundness 2: Ok(keep) stores an ALIASED payload (the caller's live
// string, inc'd at construction under EnumRcPayloads). The `?` binding takes
// one counted reference; keep must remain readable after every iteration and
// nothing may double-free.
const tryScrutAliasedPayloadSafe = `function mk(pre: string): Result[string, i32] { return Ok(pre); }
function go(pre: string): Result[i32, i32] { var s: string = mk(pre)?; return Ok(s.len()); }
function main(): i32 {
    var keep: string = "abc" + "def";
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        match (go(keep)) { Ok(v) => { acc = acc + v; }, Err(e) => { acc = acc + e; }, }
        i = i + 1;
    }
    if (acc != 1200) { return 99; }
    if (keep.len() != 6) { return 88; }
    return __rc_underflow_count();
}`

func checkTryScrutSafe(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	if _, code := run(t, tryScrutAliasedBoxSafe); code != 0 {
		t.Errorf("aliased-box safety: code=%d (99=value, >0=over-release/UAF)", code)
	}
	if _, code := run(t, tryScrutAliasedPayloadSafe); code != 0 {
		t.Errorf("aliased-payload safety: code=%d (99=value, 88=payload freed under caller, >0=over-release)", code)
	}
}

func TestX86_64TryScrutineeReclaim(t *testing.T) {
	for _, mk := range []func(string) string{tryScrutScalarBumpSrc, tryScrutOptionBumpSrc, tryScrutStringBumpSrc} {
		small := mustRunX86_64FreeOn(t, mk("50"))
		large := mustRunX86_64FreeOn(t, mk("5000"))
		if small != large {
			t.Errorf("try-scrutinee bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
		}
	}
	checkTryScrutSafe(t, compileAndRunX86_64FreeOn)
}

func TestArm64TryScrutineeReclaim(t *testing.T) {
	for _, mk := range []func(string) string{tryScrutScalarBumpSrc, tryScrutOptionBumpSrc, tryScrutStringBumpSrc} {
		small := mustRunArm64FreeOn(t, mk("50"))
		large := mustRunArm64FreeOn(t, mk("5000"))
		if small != large {
			t.Errorf("try-scrutinee bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
		}
	}
	checkTryScrutSafe(t, compileAndRunArm64FreeOn)
}

func TestWASMTryScrutineeReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	// The concat-payload string shape is bounded on natives only (pair-form
	// wasm keeps the documented payload leak — see tryScrutStringBumpSrc);
	// wasm asserts the literal-payload sibling for boundedness instead and
	// pins the concat shape as correctness + detector-zero below.
	for _, mk := range []func(string) string{tryScrutScalarBumpSrc, tryScrutOptionBumpSrc, tryScrutStrLitBumpSrc} {
		small := runWasm(t, mk("50"))
		large := runWasm(t, mk("5000"))
		if small != large {
			t.Errorf("try-scrutinee bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
		}
	}
	if code := runWasm(t, tryScrutStringWasmSound); code != 0 {
		t.Errorf("concat-payload soundness: code=%d (99=value, >0=over-release)", code)
	}
	checkTryScrutSafe(t, func(t *testing.T, src string) (string, int) {
		return "", runWasm(t, src)
	})
}
