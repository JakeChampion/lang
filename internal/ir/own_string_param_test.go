package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #8804: an `own` STRING parameter was balanced on no backend. The callee's
// release was admitted only under the two-word ABI, so a single-word x86-64
// `own` string param was never released at all; the caller's overwrite-dec
// fired regardless, so on the two-word ABIs the two releases met on one
// buffer. The two failures cancelled — the backend that did not crash was
// leaking instead.
//
// The runtime half (heap growth and `__rc_underflow_count()` on all three
// backends) is internal/e2e/rc_own_string_param_test.go.

const ownStrParamSrc = `function put(own a: string, s: string): string { a = a + s; return a; }
function main(): i32 {
    var acc: string = "";
    var i: i32 = 0;
    while (i < 3) { acc = put(acc, "12345678"); i = i + 1; }
    return acc.len();
}`

// ownStrParamLowerings lowers the source under each string ABI: wasm's
// two-word (ptrW 4), native single-word x86-64, and arm64's two-word
// override. The three disagreed about this shape, so a test that pins only
// one of them pins the wrong thing.
func ownStrParamLowerings(t *testing.T, src string) map[string]*Program {
	t.Helper()
	prevRC, prevTW := ast.RcFreeEnabled, ast.TwoWordOverride
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled, ast.TwoWordOverride = prevRC, prevTW }()

	out := map[string]*Program{}
	for _, abi := range []struct {
		name    string
		ptrW    int
		twoWord bool
	}{
		{"wasm", 4, false},
		{"x86-64", 8, false},
		{"arm64", 8, true},
	} {
		ast.TwoWordOverride = abi.twoWord
		out[abi.name] = lowerSourceWith(t, src, abi.ptrW)
	}
	return out
}

// TestOwnStringParamReleasedOnce: the callee reclaims the reference the
// caller moved in, and the caller does not release it a second time. Both
// halves on every ABI — taking either one alone is one of the two bugs.
func TestOwnStringParamReleasedOnce(t *testing.T) {
	// arm64 has no __fern_str_append, so its `a = a + s` keeps the
	// overwrite-dec that frees the superseded buffer; the ABIs that append
	// in place suppress it (the `strAppended` arm). The exit release is the
	// one both must have, and it is what was missing.
	wantPutDecs := map[string]int{"wasm": 1, "x86-64": 1, "arm64": 2}
	for abi, prog := range ownStrParamLowerings(t, ownStrParamSrc) {
		if got := countStringDecs(prog, "put"); got != wantPutDecs[abi] {
			t.Errorf("%s: string decs in put = %d, want %d (the moved-in param's exit release)", abi, got, wantPutDecs[abi])
		}
		// main's `acc = put(acc, …)` is a move, so its overwrite-dec must
		// not fire; what decs remain are acc's own scope-exit release.
		if got := countStringDecs(prog, "main"); got > 2 {
			t.Errorf("%s: string decs in main = %d, want at most 2 (the overwrite-dec must not fire on a consumed argument)", abi, got)
		}
	}
}

// TestOwnStringParamSelfAppendsInPlace: with the parameter eligible, its
// self-append is the in-place `__fern_str_append` a bare local already got.
// arm64 has no such helper (strAppendAvailable) and keeps OpStrConcat, so
// this pins only the two ABIs that emit it.
func TestOwnStringParamSelfAppendsInPlace(t *testing.T) {
	progs := ownStrParamLowerings(t, ownStrParamSrc)
	for _, abi := range []string{"wasm", "x86-64"} {
		if got := countFnCallDirect(progs[abi], "put", "__fern_str_append"); got != 1 {
			t.Errorf("%s: __fern_str_append calls in put = %d, want 1", abi, got)
		}
		if got := countOpKind(progs[abi], "put", OpStrConcat); got != 0 {
			t.Errorf("%s: OpStrConcat in put = %d, want 0 (the append replaces it)", abi, got)
		}
	}
}

// TestReassignedStringParamMatchesTheOwnShape: a plain parameter the body
// REASSIGNS is consumed-threaded (#8785), so its ops are the `own` ones above
// — the append in place and the exit release — and what keeps that sound is
// the balanced ENTRY RETAIN, which `own` does not take. The retain puts the
// incoming buffer at rc >= 2, so the first append of every call takes
// __fern_str_append's copy path and the caller's string is not lengthened
// under it; the reference the exit release spends is the retain's, not the
// caller's. rcCorpus's str_param_append_leaves_the_callers_string_alone is
// the runtime half — it reads the caller's length either side of the call.
func TestReassignedStringParamMatchesTheOwnShape(t *testing.T) {
	const src = `function put(a: string, s: string): string { a = a + s; return a; }
function main(): i32 {
    var acc: string = "";
    var i: i32 = 0;
    while (i < 3) { acc = put(acc, "12345678"); i = i + 1; }
    return acc.len();
}`
	wantPutDecs := map[string]int{"wasm": 1, "x86-64": 1, "arm64": 2}
	twoWord := map[string]bool{"wasm": true, "x86-64": false, "arm64": true}
	for abi, prog := range ownStrParamLowerings(t, src) {
		if got := countStringDecs(prog, "put"); got != wantPutDecs[abi] {
			t.Errorf("%s: string decs in put = %d, want %d (the promoted param's exit release)", abi, got, wantPutDecs[abi])
		}
		if got := paramEntryRetains(prog, "put", 0, twoWord[abi]); got != 1 {
			t.Errorf("%s: balanced entry retains on put's param 0 = %d, want 1 — without it the append grows the CALLER's buffer", abi, got)
		}
	}
	progs := ownStrParamLowerings(t, src)
	for _, abi := range []string{"wasm", "x86-64"} {
		if got := countFnCallDirect(progs[abi], "put", "__fern_str_append"); got != 1 {
			t.Errorf("%s: __fern_str_append calls in put = %d, want 1", abi, got)
		}
	}
}

// TestBorrowedStringParamStillCopies: a parameter the body only READS is
// borrowed — its buffer belongs to the caller, which reads it back after the
// call — so it may neither be grown in place nor released here. That is the
// boundary the promotion above does not cross: no reassignment, no entry
// retain, no ownership.
func TestBorrowedStringParamStillCopies(t *testing.T) {
	const src = `function put(a: string, s: string): string { return a + s; }
function main(): i32 {
    var acc: string = "";
    var i: i32 = 0;
    while (i < 3) { acc = put(acc, "12345678"); i = i + 1; }
    return acc.len();
}`
	for abi, prog := range ownStrParamLowerings(t, src) {
		if got := countFnCallDirect(prog, "put", "__fern_str_append"); got != 0 {
			t.Errorf("%s: __fern_str_append calls in put = %d, want 0 (a borrowed param's buffer is the caller's)", abi, got)
		}
		if got := countStringDecs(prog, "put"); got != 0 {
			t.Errorf("%s: string decs in put = %d, want 0 (the caller still owns it)", abi, got)
		}
	}
}
