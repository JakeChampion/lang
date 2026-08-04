package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupDiscReclaimCases pin the #4365 discarded rc-field tuple temp reclaim: a
// discarded tuple LITERAL carrying a fresh array element (`(w, [w, w+1]);`) and
// a discarded CALL to a tuple-fresh-ret free function (`mk(i);`, every return a
// direct tuple literal) both leaked box + array buffers per evaluation on the
// self-host IR path (native bounds both). The literal arm deep-drops via
// tuple_lit_rc_reclaimable (the TUPRC: admission — ExprArray positions freed,
// ident/pointer elements skipped); the call arm consults the "TUPRET:<name>|
// <flags>" registry (tuple_ret_arrfree_flags: '1' where EVERY return has a
// direct array literal under a scalar-element-array annotation; an i64/u64/f64
// SCALAR element declines the whole entry for tuple_get layout uniformity).
var tupDiscReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// Core churn: both discarded shapes rebuilt per iteration, heap bounded.
	{"tupdisc-churn-flat", `function mk(i: i32): (i32[], i32) {
    return ([i, i + 1], i);
}
function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { (w, [w, w + 1]); mk(w); w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { (i, [i, i + 1]); mk(i); i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Negatives in one program, all CORRECT at detector zero: an IDENT element
	// aliases a live local (skipped by the literal deep-drop — xs stays valid);
	// a MIXED-return callee (array literal in one branch, ident in the other)
	// gets flag '0' — box-only free; a STRING element is a pointer slot — the
	// array position + box are freed, the string stays leak-mode; an i64
	// element declines the entire entry (offset-uniform tuple_get).
	{"tupdisc-negatives-safe", `function mkmix(i: i32, flip: i32): (i32[], i32) {
    var ys: i32[] = [i, i, i];
    if (flip > 0) { return ([i, i + 1], i); }
    return (ys, i);
}
function mkstr(i: i32): (string, i32[]) {
    return ("v" + "w", [i, i + 1]);
}
function mki64(i: i32): (i64, i32[]) {
    return (7, [i, i + 1]);
}
function main(): i32 {
    var xs: i32[] = [7, 8];
    var k: i32 = 3;
    (k, xs);
    var alias_ok: i32 = xs[0] + xs[1];
    if (alias_ok != 15) { return 97; }
    var w: i32 = 0;
    while (w < 50) { mkmix(w, w % 2); mkstr(w); mki64(w); w = w + 1; }
    var again: i32 = xs[0] + xs[1];
    if (again != 15) { return 96; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// BOUND calls are untouched by the TUPRET registry (it fires only on a
	// discarded statement): a bound result stays readable after a discarded
	// call to the same callee, values exact.
	{"tupdisc-bound-untouched", `function mk(i: i32): (i32[], i32) {
    return ([i, i + 1], i);
}
function main(): i32 {
    var t = mk(3);
    mk(9);
    var v: i32 = t.0[0] + t.0[1] + t.1;
    if (v != 10) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return v;
}`, 10},
}

// TestSelfHostTupDiscReclaimIRX86_64 drives the cases through the self-hosted
// x86-64 compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostTupDiscReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range tupDiscReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = tuple temp leaked; 99 = over-release/underflow; 96-97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
