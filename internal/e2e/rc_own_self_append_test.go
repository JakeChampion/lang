package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// `own` array parameters that self-append in a loop — `p = p.append(x)` — and
// return the grown buffer. An `own` param is callee-owned (the caller
// transferred it; no entry-inc; uniquely owned at rc==1 when the E051-guarded
// arg was fresh), so its self-append is rc-gated exactly like an owned local:
// in-place at rc==1, the orphan freed on grow. Before the gate extension an
// `own` param was excluded from `isSelfArrayPushLocal`, orphaning one buffer
// per iteration; now it reclaims grow intermediates.
//
// These pin the SAFETY contract — correct values across grows AND no
// over-release (`__rc_underflow_count() == 0`, i.e. the buffer-only
// `__fern_arr_dec` never double-frees a shared element) — for i32[] and the
// string[] case where a flat element-walking dec would be unsound. This is the
// runtime half of move semantics for threaded array params
// (docs/RC-ARRAY-MOVE-SEMANTICS-PLAN.md step 3).
const ownSelfAppendSrc = `
function build_i32(own p: i32[], n: i32): i32[] {
    var i: i32 = 0;
    while (i < n) { p = p.append(i * 2 + 1); i = i + 1; }
    return p;
}
function build_str(own p: string[], n: i32): string[] {
    var i: i32 = 0;
    while (i < n) { p = p.append("x"); i = i + 1; }
    return p;
}
function main(): i32 {
    var k: i32 = 0;
    while (k < 200) {
        var a: i32[] = build_i32([], 120);
        if (a.len() != 120) { return 10; }
        if (a[0] != 1) { return 11; }
        if (a[119] != 239) { return 12; }      // integrity across grows
        var s: string[] = build_str(["seed"], 60);
        if (s.len() != 61) { return 20; }
        k = k + 1;
    }
    return __rc_underflow_count();              // 0 == no over-release / double-free
}`

func TestX86_64OwnSelfAppend(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, ownSelfAppendSrc); code != 0 {
		t.Errorf("own-param self-append: got %d, want 0", code)
	}
}

func TestArm64OwnSelfAppend(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, ownSelfAppendSrc); code != 0 {
		t.Errorf("own-param self-append: got %d, want 0", code)
	}
}

func TestWASMOwnSelfAppend(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, ownSelfAppendSrc); got != 0 {
		t.Errorf("own-param self-append: got %d, want 0", got)
	}
}
