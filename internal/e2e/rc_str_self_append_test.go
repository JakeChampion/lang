package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #5637 option 3 — end-to-end payoff and safety of the in-place string
// self-append (`s = s + piece` → __fern_str_append). The LOWERING decision is
// pinned target-independently in internal/ir/rc_str_self_append_test.go; these
// pin what the emitted runtime actually does.

// strSelfAppendLoopSrc grows a string 2 bytes at a time to 4000 bytes, the
// shape `std/unicode`'s _map_case and `std/utf8`'s encode_all use per code
// point. Returns 0 on the expected length so a miscompile shows as a non-zero
// exit rather than a silently wrong allocation count.
const strSelfAppendLoopSrc = `function main(): i32 {
    var s: string = "";
    var i: i32 = 0;
    while (i < 2000) {
        s = s + "ab";
        i = i + 1;
    }
    if (s.len() != 4000) { return 1; }
    return 0;
}`

// TestX86_64StrSelfAppendAllocsBounded pins the allocation collapse through the
// leak detector, which counts every __fern_alloc / __fern_free (see
// leakcheck_test.go). Measured on this exact program:
//
//	before -> allocs=1997 frees=1    live_bytes=4031936
//	after  -> allocs= 250 frees=250  live_bytes=0
//
// Two things changed. The appends now mostly land in the buffer's existing
// 16-byte size-class slack, so one allocation covers ~8 of them instead of one
// each; and the fallback path releases the old buffer through __fern_str_dec
// (which frees at rc==1) where the suppressed dec-on-overwrite used the native
// __fern_rc_dec, which decrements but never frees — so the accumulator's
// intermediates were leaked outright, 4 MB of them here.
//
// The assertions are the invariants, not the exact numbers: one allocation per
// class step rather than per append (allocs well under the iteration count),
// and a balanced heap at exit (allocs == frees, live_bytes == 0) — the latter
// is what catches an over-release just as firmly as a leak.
func TestX86_64StrSelfAppendAllocsBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckX86_64(t, strSelfAppendLoopSrc)
	if code != 0 {
		t.Fatalf("string self-append loop exited %d (want 0 — the accumulated length was wrong); stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	// 2000 appends over ~4000 bytes cross ~250 16-byte classes. Anything near
	// the iteration count means the in-place path never fired.
	if allocs > 400 {
		t.Errorf("allocs = %d for 2000 appends, want <= 400 (~one per 16-byte class step); the in-place append is not firing", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced after the append loop: allocs=%d frees=%d live_bytes=%d, want allocs==frees and live_bytes==0", allocs, frees, live)
	}
}

// strSelfAppendCorrectnessSrc exercises the shapes the in-place path must get
// right, all against values the interpreter agrees on:
//
//   - plain growth across the SSO -> heap boundary and several class steps,
//   - an ALIASED accumulator: `alias` holds the buffer while `s` grows, so the
//     alias inc puts it at rc>1 and the append MUST copy instead of mutating
//     the value `alias` still reads,
//   - self-concat (`e = e + e`), where source and destination are one buffer,
//   - appending the empty string (a zero-byte copy that must not disturb the
//     length or the class test).
const strSelfAppendCorrectnessSrc = `function build(n: i32, piece: string): string {
    var s: string = "";
    var i: i32 = 0;
    while (i < n) {
        s = s + piece;
        i = i + 1;
    }
    return s;
}

function main(): i32 {
    print(build(5, "ab"));
    print(build(40, "xyz"));
    print("[" + build(3, "") + "]");
    var d: string = "";
    var i: i32 = 0;
    while (i < 6) {
        var alias: string = d;
        d = d + "q";
        print(alias + "|" + d);
        i = i + 1;
    }
    var e: string = "ab";
    var k: i32 = 0;
    while (k < 5) {
        e = e + e;
        k = k + 1;
    }
    print(e);
    return 0;
}`

// The trailing newline of the final print is omitted: runWasmCapturingStdout
// trims it, and the native comparison adds it back.
const strSelfAppendWant = `ababababab
xyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyz
[]
|q
q|qq
qq|qqq
qqq|qqqq
qqqq|qqqqq
qqqqq|qqqqqq
abababababababababababababababababababababababababababababababab`

// TestWASMStrSelfAppendCorrect runs the shapes above on the two-word (wasm)
// ABI, where the in-place path returns (a_data, la+lb) with the buffer's rc
// left at 1.
func TestWASMStrSelfAppendCorrect(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	if got := runWasmCapturingStdout(t, strSelfAppendCorrectnessSrc); got != strSelfAppendWant {
		t.Errorf("wasm string self-append output =\n%q\nwant\n%q", got, strSelfAppendWant)
	}
}

// TestX86_64StrSelfAppendCorrect is the native single-word sibling, where the
// in-place path also restamps the length prefix at [data-4] and the trailing
// NUL. Runs under the leak detector so the same program doubles as an
// over-release probe: a buffer freed while still aliased would show up as
// frees > allocs (or as corrupted output).
func TestX86_64StrSelfAppendCorrect(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckX86_64(t, strSelfAppendCorrectnessSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != strSelfAppendWant+"\n" {
		t.Errorf("x86-64 string self-append output =\n%q\nwant\n%q", stdout, strSelfAppendWant+"\n")
	}
	if allocs, frees, _ := parseLeakCheckLine(t, stderr); frees > allocs {
		t.Errorf("frees=%d > allocs=%d — the append over-released a buffer", frees, allocs)
	}
}
