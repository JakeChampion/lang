package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostTryBoxReclaimIRX86_64 pins the #4355 `?`-consumed source-box
// reclaim on the self-host IR path: `mk(pre)?` over an OPT-FRESH producer
// (every return in the callee a direct Ok/Err/Some/None constructor —
// opt_fresh_ret_fns_of) frees the dead source box at both consume edges
// (success after the payload read; the Option failure edge before the fresh
// None), and a FRESH string success payload's sole reference MOVES to the
// `var s: string = ...?` binding, credited "STR:" so the exit sweep frees it
// (collect_try_str_binding_names).
//
// The self-host does NOT yet reclaim a call-result Option/Result box consumed
// by the CALLER's match (the outer `inner(pre)` box leaks — a pre-existing
// class), so absolute flatness can't isolate the `?` edge. Each churn case
// instead compares a hand-desugared BASELINE (innerB — the same consume via
// match, which leaks the source box) against the `?` version (innerT) in one
// program, baseline FIRST so freelist reuse can't flatter it: with the fix
// the try churn's growth is at most HALF the baseline's (the source box —
// and, for the string case, the payload — recycle); without it the two are
// equal and the assertion fails. Values cross-checked, over-release by the
// __rc_underflow detector.
func TestSelfHostTryBoxReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (98 = try churn leaked as much as baseline → box not reclaimed; 99 = over-release; 97 = value corrupted; 88 = aliased payload freed under caller)", name, code, want)
		}
	}

	// SCALAR Result payload: the try churn leaks only the outer box; the
	// baseline additionally leaks mk's source box.
	run(t, `function mk(pre: string): Result[i32, i32] { return Ok(pre.len()); }
function innerT(pre: string): Result[i32, i32] { var v: i32 = mk(pre)?; return Ok(v + 1); }
function innerB(pre: string): Result[i32, i32] { var t: i32 = 0; match (mk(pre)) { Ok(v) => { t = v + 1; }, Err(e) => { t = e; }, } return Ok(t); }
function churnT(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { var r = innerT(pre); match (r) { Ok(k) => { acc = (acc + k) % 251; }, Err(e) => { acc = e; }, } i = i + 1; } return acc; }
function churnB(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { var r = innerB(pre); match (r) { Ok(k) => { acc = (acc + k) % 251; }, Err(e) => { acc = e; }, } i = i + 1; } return acc; }
function main(): i32 {
    var b0: i32 = (__heap_bump_bytes() as i32);
    var w: i32 = churnB(3000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churnT(3000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    var gb: i32 = b1 - b0;
    var gt: i32 = b2 - b1;
    if (gt + gt > gb + 256) { return 98; }
    return 0;
}`, "try-box-scalar-ratio", 0)

	// STRING success payload: the `?` edge frees the box AND the binding
	// frees the moved concat payload — the try churn recycles both, so its
	// growth is well under half the baseline's (box + string leak there).
	run(t, `function mk(pre: string): Result[string, i32] { return Ok(pre + "abc"); }
function innerT(pre: string): Result[i32, i32] { var s: string = mk(pre)?; return Ok(s.len()); }
function innerB(pre: string): Result[i32, i32] { var t: i32 = 0; match (mk(pre)) { Ok(s) => { t = s.len(); }, Err(e) => { t = e; }, } return Ok(t); }
function churnT(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { var r = innerT(pre); match (r) { Ok(k) => { acc = (acc + k) % 251; }, Err(e) => { acc = e; }, } i = i + 1; } return acc; }
function churnB(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { var r = innerB(pre); match (r) { Ok(k) => { acc = (acc + k) % 251; }, Err(e) => { acc = e; }, } i = i + 1; } return acc; }
function main(): i32 {
    var b0: i32 = (__heap_bump_bytes() as i32);
    var w: i32 = churnB(3000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churnT(3000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    var gb: i32 = b1 - b0;
    var gt: i32 = b2 - b1;
    if (gt + gt > gb + 256) { return 98; }
    return 0;
}`, "try-box-string-ratio", 0)

	// OPTION with failure alternation: the failure edge frees the fresh None
	// source box before propagating a new None.
	run(t, `function mko(pre: string, k: i32): Option[i32] { if (k > 0) { return None; } return Some(pre.len()); }
function innerT(pre: string, k: i32): Option[i32] { var v: i32 = mko(pre, k)?; return Some(v + 1); }
function innerB(pre: string, k: i32): Option[i32] { var t: i32 = 0; var got: i32 = 0; match (mko(pre, k)) { Some(v) => { t = v + 1; got = 1; }, None => {}, } if (got == 0) { return None; } return Some(t); }
function churnT(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { var r = innerT(pre, i % 2); match (r) { Some(k) => { acc = (acc + k) % 251; }, None => { acc = (acc + 1) % 251; }, } i = i + 1; } return acc; }
function churnB(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { var r = innerB(pre, i % 2); match (r) { Some(k) => { acc = (acc + k) % 251; }, None => { acc = (acc + 1) % 251; }, } i = i + 1; } return acc; }
function main(): i32 {
    var b0: i32 = (__heap_bump_bytes() as i32);
    var w: i32 = churnB(3000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churnT(3000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    var gb: i32 = b1 - b0;
    var gt: i32 = b2 - b1;
    if (gt + gt > gb + 256) { return 98; }
    return 0;
}`, "try-box-option-failure-ratio", 0)

	// ALIASED payload excluded: mk forwards its param (`Ok(pre)`) — a
	// non-fresh payload, so the producer is flagged "a": the box still frees
	// but the binding takes NO string ownership. keep must stay readable and
	// nothing double-frees.
	run(t, `function mk(pre: string): Result[string, i32] { return Ok(pre); }
function inner(pre: string): Result[i32, i32] { var s: string = mk(pre)?; return Ok(s.len()); }
function go(pre: string): i32 { var r: i32 = 0; match (inner(pre)) { Ok(k) => { r = k; }, Err(e) => { r = e; }, } return r; }
function main(): i32 { var keep: string = "abc" + "def"; var bad: i32 = 0; var i: i32 = 0; while (i < 1000) { if (go(keep) != 6) { bad = 1; } i = i + 1; } if (keep.len() != 6) { return 88; } if (__rc_underflow() != 0) { return 99; } return bad; }`,
		"try-aliased-payload-excluded", 0)

	// NON-CTOR return excluded: pass() forwards a live box (`return r`), so
	// it is not OPT-FRESH — the `?` edge must NOT free it (b stays valid and
	// re-readable after every call). Correctness + detector only.
	run(t, `function pass(r: Result[string, i32]): Result[string, i32] { return r; }
function inner(b: Result[string, i32]): Result[i32, i32] { var s: string = pass(b)?; return Ok(s.len()); }
function main(): i32 {
    var b: Result[string, i32] = Ok("hi" + "!");
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 1000) {
        match (inner(b)) { Ok(k) => { if (k != 3) { bad = 1; } }, Err(e) => { bad = e; }, }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return bad;
}`, "try-nonfresh-callee-excluded", 0)

	// ESCAPING binding excluded: s escapes via `return Ok(s)`, so the "STR:"
	// credit is rejected (body_unsafe_for) — the returned string must stay
	// valid at the caller. Box still freed; correctness + detector only.
	run(t, `function mk(pre: string): Result[string, i32] { return Ok(pre + "abc"); }
function inner(pre: string): Result[string, i32] { var s: string = mk(pre)?; return Ok(s); }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 1000) {
        match (inner("ab")) { Ok(s) => { if (s.len() != 5) { bad = 1; } }, Err(e) => { bad = e; }, }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return bad;
}`, "try-escaping-binding-excluded", 0)
}
