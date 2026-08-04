package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Enum-payload-struct STRING-field reclamation (#4355): a local bound to a
// fresh constructor result whose payload struct carries a STRING field
// (`var e: E = mk(i); match (e) { … }` with `enum E { A(S), B }`,
// `struct S { name: string, n: i32 }`) leaked the WHOLE chain — enum box +
// payload struct box + its string — once per iteration, while the identical
// scalar- or array-field payload reclaimed fine.
//
// Root cause was in the ANALYSIS, not the drop machinery: exprNoParamEscape
// had no case for string literals / concats, so any constructor embedding a
// string field lost its returnsNoParamEscape verdict; rhsTainted's call case
// then fell through to the generic any-arg-tainted rule, a noise-tainted
// scalar arg poisoned the call result, and the local was never swept. The fix
// teaches exprNoParamEscape the three provenance-free-fresh string shapes
// (literal = static sentinel; concat = byte-copy into a fresh buffer; string
// slice = fresh copy, not a view) — the same rule rhsTainted's IsStringConcat
// case already encodes. The reclaim itself rides the existing, proven route:
// freeEligible → emitVarReinitDropOld → __drop_enum_<E> → __drop_struct_<S>
// (string fields dec'd inline) — the machinery the scalar-payload sibling has
// exercised all along.
//
// A constructor that embeds a PARAM string (`S { name: nm }`, nm a param)
// still fails the verdict and keeps the prior safe leak — pinned below.

// Bounded churn 1: pair-form-shaped enum (single payload + sentinel), string
// field from a LITERAL — the ep-probe shape that leaked box+box+string.
func enumPayloadStructStrLitBumpSrc(n string) string {
	return `struct S { name: string, n: i32 }
enum E { A(S), B }
function mk(n: i32): E { return A(S { name: "ab", n: n }); }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < ` + n + `) {
        var e: E = mk(i);
        match (e) { A(s) => { acc = acc + s.n; }, B => { acc = acc + 0; } }
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    if (__rc_underflow_count() != 0) { return -2; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Bounded churn 2: the string field is a RUNTIME CONCAT (fresh heap string
// per construction), payload struct + a second scalar payload so the enum is
// heap-boxed on every backend.
func enumPayloadStructStrConcatBumpSrc(n string) string {
	return `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E { return A(S { name: nm + "x", n: n }, n); }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < ` + n + `) {
        var e: E = mk("a", i);
        match (e) { A(s, k) => { acc = acc + k + s.name.len(); }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    if (__rc_underflow_count() != 0) { return -2; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// SOUNDNESS 1: a constructor that embeds its PARAM string into the field is
// NOT escape-free — the local must keep the prior safe leak, the caller's
// string stays readable across every iteration, detector zero.
const enumPayloadStructStrParamEmbedSafe = `struct S { name: string, n: i32 }
enum E { A(S), B }
function mk(nm: string, n: i32): E { return A(S { name: nm, n: n }); }
function main(): i32 {
    var keep: string = "aa" + "bb";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var e: E = mk(keep, i);
        match (e) { A(s) => { acc = acc + s.name.len(); }, B => { acc = acc + 0; } }
        i = i + 1;
    }
    if (keep.len() != 4) { return 88; }
    if (acc != 2000) { return 99; }
    return __rc_underflow_count();
}`

// SOUNDNESS 2: a field READ extracted from the payload (`last = s.name`) is a
// counted alias — it must survive the next iteration's reinit deep-drop of
// the previous box chain, detector zero.
const enumPayloadStructStrAliasedReadSafe = `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E { return A(S { name: nm + "xy", n: n }, n); }
function main(): i32 {
    var last: string = "";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var e: E = mk("a", i);
        match (e) { A(s, k) => { last = s.name; acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    if (last.len() != 3) { return 88; }
    if (acc < 0) { return 99; }
    return __rc_underflow_count();
}`

// SOUNDNESS 3: an ALIASED box (id2 returns its param; rc >= 2 via the
// return-transfer inc) must only be dec'd at the reinit — never freed under
// the alias. Values stay right, detector zero.
const enumPayloadStructStrAliasedBoxSafe = `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E { return A(S { name: nm + "x", n: n }, n); }
function id2(e: E): E { return e; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var e: E = id2(mk("a", i));
        match (e) { A(s, k) => { acc = acc + s.name.len(); }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    if (acc != 400) { return 99; }
    return __rc_underflow_count();
}`

func checkEnumPayloadStructStrSafe(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	if _, code := run(t, enumPayloadStructStrParamEmbedSafe); code != 0 {
		t.Errorf("param-embed safety: code=%d (88=caller string freed, 99=value, >0=over-release)", code)
	}
	if _, code := run(t, enumPayloadStructStrAliasedReadSafe); code != 0 {
		t.Errorf("aliased-read safety: code=%d (88=read freed under alias, 99=value, >0=over-release)", code)
	}
	if _, code := run(t, enumPayloadStructStrAliasedBoxSafe); code != 0 {
		t.Errorf("aliased-box safety: code=%d (99=value, >0=over-release)", code)
	}
}

func TestX86_64EnumPayloadStructStrReclaim(t *testing.T) {
	for _, mk := range []func(string) string{enumPayloadStructStrLitBumpSrc, enumPayloadStructStrConcatBumpSrc} {
		small := mustRunX86_64FreeOn(t, mk("50"))
		large := mustRunX86_64FreeOn(t, mk("5000"))
		if small != large {
			t.Errorf("enum-payload-struct string bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
		}
	}
	checkEnumPayloadStructStrSafe(t, compileAndRunX86_64FreeOn)
}

func TestArm64EnumPayloadStructStrReclaim(t *testing.T) {
	for _, mk := range []func(string) string{enumPayloadStructStrLitBumpSrc, enumPayloadStructStrConcatBumpSrc} {
		small := mustRunArm64FreeOn(t, mk("50"))
		large := mustRunArm64FreeOn(t, mk("5000"))
		if small != large {
			t.Errorf("enum-payload-struct string bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
		}
	}
	checkEnumPayloadStructStrSafe(t, compileAndRunArm64FreeOn)
}

// Wasm-only correctness pin for the CONCAT-field shape: on wasm32 the enum
// and struct boxes reclaim but the two-word CONCAT string field keeps a
// documented sound leak (~one string box per iteration — the string-field
// ownership discipline in enum-payload structs is the wasm follow-up, the
// sibling of tryScrutStringWasmSound's pair-form payload exclusion). Values
// stay right and nothing over-releases.
const enumPayloadStructStrConcatWasmSound = `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E { return A(S { name: nm + "x", n: n }, n); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 2000) {
        var e: E = mk("a", i);
        match (e) { A(s, k) => { acc = acc + s.name.len(); }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    if (acc != 4000) { return 99; }
    return __rc_underflow_count();
}`

func TestWASMEnumPayloadStructStrReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	// The LITERAL-field shape is fully bounded on wasm (boxes reclaim, the
	// literal payload is static). The CONCAT-field shape keeps a documented
	// sound string-field leak on wasm32 — see the WasmSound pin below.
	small := runWasm(t, enumPayloadStructStrLitBumpSrc("50"))
	large := runWasm(t, enumPayloadStructStrLitBumpSrc("5000"))
	if small != large {
		t.Errorf("enum-payload-struct string bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if code := runWasm(t, enumPayloadStructStrConcatWasmSound); code != 0 {
		t.Errorf("concat-field soundness: code=%d (99=value, >0=over-release)", code)
	}
	checkEnumPayloadStructStrSafe(t, func(t *testing.T, src string) (string, int) {
		return "", runWasm(t, src)
	})
}
