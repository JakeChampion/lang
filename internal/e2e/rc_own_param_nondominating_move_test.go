// Move-on-call claims an `own` param whole-function from its textually-last
// occurrence and silences the exit sweep on EVERY path — sound only where the
// transfer dominates every exit, which nothing checked (#8146).
//
// The two shapes below are what a non-dominating claim costs at run time. Both
// are silent: the program's answer is right either way, and the leak detector
// is the only thing that sees the second one at all.
//
//   - a return that hands the param back instead of transferring it keeps its
//     return-transfer inc while losing the sweep dec that balanced it, so the
//     box is retained one time too many per call. The stale box holds a
//     reference to every rc-tracked field, so the NEXT append to one sees the
//     buffer at rc 2 and copies it whole — quadratic bytes, correct output.
//     That is the x86 assembler's 16 MB `.text` buffer taking a 32 MB copy
//     every other instruction, and the 16 GiB arena it exhausted.
//   - an exit that neither transfers nor returns the param loses the sweep
//     with nothing to replace it, which is a plain leak of the whole box.
package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// ownParamNonDominatingCliffSrc is the reduced #8146 shape: an `own` param
// self-updated and returned on one branch, transferred to another `own`
// parameter on the other. The transfer is the textually-last occurrence, so it
// is what move-on-call claims, but the loop only ever takes the FIRST branch.
// Every append must still mutate in place.
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
    var s: S = S { code: [], n: 0 };
    var i: i32 = 0;
    while (i < 200) { s = emit(s, 1); i = i + 1; }
    if (s.code.len() != 200) { return 254; }
    if (s.code[7] != 1 || s.code[199] != 1) { return 253; }
    return __arr_push_shared_count();
}`

// ownParamNonDominatingLeakSrc is the other arm: the early exit returns a
// scalar, so the param is neither transferred nor handed back on that path.
// Under the unguarded claim its box was never released — 100% of the structs
// this loop builds.
const ownParamNonDominatingLeakSrc = `struct S { code: i32[], n: i32 }
function bump(own s: S, v: i32): S { return S { ...s, n: s.n + v }; }
function emit(own s: S, v: i32): i32 {
    if (v > 0) { return 7; }
    var t: S = bump(s, v);
    return t.n;
}
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
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
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, ownParamNonDominatingCliffSrc); got != 0 {
		t.Errorf("wasm own param returned on one branch and transferred on another: "+
			"__arr_push_shared_count() = %d, want 0", got)
	}
}
