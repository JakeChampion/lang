package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupleRetIntermediateCases pin the #4357 tuple-returning dead-intermediate
// reclaim: `var t = mk(k); … t.0 …` where mk's every return is a DIRECT tuple
// literal leaked one box per call on the self-host IR path (native reclaims
// it). tuple_fresh_ret_fns_of now registers such free functions, and
// reclaimable_names_of credits their non-reassigned, non-escaping call
// bindings into the existing "TUP:" class — the same SHALLOW box free a
// scalar-tuple literal local gets (elements untouched), at scope exit and at
// the loop-rebind / precise-drop sites. Box freshness is the only admission
// requirement (the box is constructed in return position, never bound in the
// callee); element shapes don't matter to a shallow free. Escape and alias
// shapes stay un-credited (leak-mode) and must remain CORRECT at detector
// zero.
var tupleRetIntermediateCases = []struct {
	name string
	src  string
	want int
}{
	// The core leak shape: bind, read elements, dead at exit.
	{"tupret-flat", `function mk(k: i32): (i32, i32) { return (k, k + 1); }
function go(k: i32): i32 { var t = mk(k); return t.0 + t.1; }
function churn(m: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < m) { acc = (acc + go(i)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w: i32 = churn(3000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(3000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
	// Loop-local rebind in one frame — per-iteration boxes reclaim at the rebind.
	{"tupret-loop-rebind-flat", `function mk(k: i32): (i32, i32) { return (k, k + 1); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3000) { var t = mk(i); acc = (acc + t.0 + t.1) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 3000) { var t2 = mk(j); acc = (acc + t2.0 + t2.1) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 256) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Multi-return callee: every branch a direct literal still qualifies.
	{"tupret-branchy-flat", `function mk(k: i32): (i32, i32) { if (k % 2 == 0) { return (k, k + 1); } return (k + 1, k); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3000) { var t = mk(i); acc = (acc + t.0 + t.1) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 3000) { var t2 = mk(j); acc = (acc + t2.0 + t2.1) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 256) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// ESCAPE negative: `return t` un-credits t (a returned box must not be
	// freed under the caller). Value correctness + detector zero.
	{"tupret-escape-safe", `function mk(k: i32): (i32, i32) { return (k, k + 1); }
function wrap(k: i32): (i32, i32) { var t = mk(k); return t; }
function main(): i32 { var u = wrap(20); if (__rc_underflow() != 0) { return 99; } return u.0 + u.1; }`, 41},
	// ALIAS negative: `var u = t` un-credits t (freeing would dangle u).
	{"tupret-alias-safe", `function mk(k: i32): (i32, i32) { return (k, k + 1); }
function main(): i32 { var t = mk(20); var u = t; var a: i32 = t.0 + u.1; if (__rc_underflow() != 0) { return 99; } return a; }`, 41},
}

// TestSelfHostTupleRetIntermediateIRX86_64 drives the cases through the
// self-hosted x86-64 compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostTupleRetIntermediateIRX86_64(t *testing.T) {
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

	for _, tc := range tupleRetIntermediateCases {
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
				t.Errorf("%s = %d, want %d (98 = box leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
