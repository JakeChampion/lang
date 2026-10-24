package e2e

import (
	"strings"
	"testing"
)

const ownParamNonDominatingCliffSrc = `struct S { code: i32[], n: i32 }
function push(buf: i32[], v: i32): i32[] { return buf.append(v); }
function bump(own s: S, v: i32): S { return S { ...s, n: s.n + v }; }
function emit(own s: S, v: i32): S {
    if (v > 0) {
        s = S { ...s, code: push(s.code, v) };
        return s;
    }
    return bump(s, v);
}
function main(): i32 {
    let s: S = S { code: [], n: 0 };
    let i: i32 = 0;
    while (i < 200) { s = emit(s, 1); i = i + 1; }
    if (s.code.len() != 200) { return 254; }
    if (s.code[7] != 1 || s.code[199] != 1) { return 253; }
    return __arr_push_shared_count();
}`

const ownParamNonDominatingLeakSrc = `struct S { code: i32[], n: i32 }
function bump(own s: S, v: i32): S { return S { ...s, n: s.n + v }; }
function emit(own s: S, v: i32): i32 {
    if (v > 0) { return 7; }
    let t: S = bump(s, v);
    return t.n;
}
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 100) { acc = acc + emit(S { code: [1, 2, 3], n: 0 }, 1); i = i + 1; }
    if (acc != 700) { return 254; }
    return 0;
}`

func TestX86_64OwnParamNonDominatingMove(t *testing.T) {
	if _, got := compileAndRunX86_64FreeOn(t, ownParamNonDominatingCliffSrc); got != 0 {
		t.Errorf("x86-64 own param returned on one branch and transferred on another: "+
			"__arr_push_shared_count() = %d, want 0 — the whole-function move claim is "+
			"silencing the sweep on a path that hands nothing away, so the retained box "+
			"puts the accumulator's next append at rc 2 (#8146)", got)
	}
	_, stderr, code := runLeakCheckX86_64(t, ownParamNonDominatingLeakSrc)
	if code != 0 {
		t.Fatalf("x86-64 leak-shape program exited %d, want 0\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "live_bytes=0") {
		t.Errorf("x86-64 own param dropped on an exit that neither transfers nor returns it: "+
			"leak report %q wants live_bytes=0 — the move claim removed the sweep on a path "+
			"the transfer never reaches", strings.TrimSpace(stderr))
	}
}

func TestArm64OwnParamNonDominatingMove(t *testing.T) {
	if _, got := compileAndRunArm64(t, ownParamNonDominatingCliffSrc); got != 0 {
		t.Errorf("arm64 own param returned on one branch and transferred on another: "+
			"__arr_push_shared_count() = %d, want 0", got)
	}
	_, stderr, code := runLeakCheckArm64(t, ownParamNonDominatingLeakSrc)
	if code != 0 {
		t.Fatalf("arm64 leak-shape program exited %d, want 0\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "live_bytes=0") {
		t.Errorf("arm64 own param dropped on an exit that neither transfers nor returns it: "+
			"leak report %q wants live_bytes=0", strings.TrimSpace(stderr))
	}
}

func TestWASMOwnParamNonDominatingMove(t *testing.T) {
	if got := runWasm(t, ownParamNonDominatingCliffSrc); got != 0 {
		t.Errorf("wasm own param returned on one branch and transferred on another: "+
			"__arr_push_shared_count() = %d, want 0", got)
	}
}
