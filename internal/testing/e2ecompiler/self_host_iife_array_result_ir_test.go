package e2ecompiler

import (
	"os/exec"
	"testing"
)

// A match-EXPRESSION whose arm hands back a bare ARRAY payload binding must
// lower (#7686), though the combination once refused with
//
//	FERN_STRICT_IR: rd (did not lower: immediately-invoked value block …)
//
// while match-expressions, arrays, and payload bindings each lowered alone.
//
// ONE result carries both a BORROWED payload (`E.A(xs) => xs`, a buffer the
// enum box still owns) and a FRESH sibling (`E.B => [i]`), so the borrowed
// buffer must never be released through the result: `iife-array-no-underflow`
// holds that half.
//
// Every case runs under FERN_STRICT_IR=1 (via runCaptureStrictIR), so a
// refusal fails the case and names the function. Every `exit` is the answer
// `bin/fern -interp` produces for that program.
var iifeArrayResultCases = []struct {
	name string
	src  string
	exit int
}{
	// The issue's repro: one borrowed-payload arm, one fresh-literal arm.
	{"iife-array-payload-mixed", `enum E { A(i32[]), B }
function rd(e: E, i: i32): i32[] { return (match (e) { E.A(xs) => xs, E.B => [i] }); }
function main(): i32 { return rd(E.A([1, 2]), 0).len() + rd(E.B, 5).len(); }`, 3},

	// The 8-byte-stride element kinds, which read at the wrong width if the
	// result is typed as a 4-byte-element array.
	{"iife-array-payload-f64", `enum E { A(f64[]), B }
function rd(e: E): f64[] { return (match (e) { E.A(xs) => xs, E.B => [1.5] }); }
function main(): i32 { return rd(E.A([1.5, 2.5])).len() + rd(E.B).len(); }`, 3},
	{"iife-array-payload-i64", `enum E { A(i64[]), B }
function rd(e: E): i64[] { return (match (e) { E.A(xs) => xs, E.B => [1i64] }); }
function main(): i32 { return rd(E.A([1i64, 2i64])).len() + rd(E.B).len(); }`, 3},

	// The result BOUND and then read through: this is what the array marking on
	// the temp buys — without it `v[0]` / `v.len()` resolve against the wrong
	// shape rather than merely leaking.
	{"iife-array-bound-and-read", `enum E { A(i32[]), B }
function rd(e: E, i: i32): i32 { let v: i32[] = (match (e) { E.A(xs) => xs, E.B => [i] }); return v[0] + v.len(); }
function main(): i32 { return rd(E.A([4, 5]), 0) + rd(E.B, 6); }`, 13},

	// THE SAFE HALF. The borrowed arm's buffer belongs to the enum box; a temp
	// that swept it would over-release, and an over-release is invisible to a
	// leak census (it balances). Only the underflow counter sees it, so this
	// returns the counter itself.
	{"iife-array-no-underflow", `enum E { A(i32[]), B }
function rd(e: E, i: i32): i32[] { return (match (e) { E.A(xs) => xs, E.B => [i] }); }
function main(): i32 {
    let i: i32 = 0;
    while (i < 30) { let k: E = E.A([i, i + 1]); if (rd(k, i).len() != 2) { return 90; } if (rd(E.B, i).len() != 1) { return 91; } i = i + 1; }
    return __rc_underflow_count();
}`, 0},

	// The DEEPER-RELEASE element kinds (#7686's remaining half): a string or
	// struct element, whose release would be an element walk. The borrowed
	// payload is never released through the result, so no walk runs; the reads
	// below check the element kind reaches the binding.
	{"iife-strarr-payload", `enum E { A(string[]), B }
function rd(e: E): string[] { return (match (e) { E.A(xs) => xs, E.B => ["z"] }); }
function main(): i32 { return rd(E.A(["a", "b"])).len() + rd(E.B).len(); }`, 3},
	{"iife-structarr-payload", `struct Q { k: i32 }
enum E { A(Q[]), B }
function rd(e: E): Q[] { return (match (e) { E.A(xs) => xs, E.B => [Q { k: 1 }] }); }
function main(): i32 { return rd(E.A([Q { k: 1 }, Q { k: 2 }])).len() + rd(E.B).len(); }`, 3},
	{"iife-strarr-bound-and-read", `enum E { A(string[]), B }
function rd(e: E): i32 { let v: string[] = (match (e) { E.A(xs) => xs, E.B => ["zz"] }); return v[0].len() + v.len(); }
function main(): i32 { return rd(E.A(["abc", "d"])) + rd(E.B); }`, 8},
	{"iife-structarr-bound-and-field", `struct Q { k: i32 }
enum E { A(Q[]), B }
function rd(e: E): i32 { let v: Q[] = (match (e) { E.A(xs) => xs, E.B => [Q { k: 5 }] }); return v[0].k + v.len(); }
function main(): i32 { return rd(E.A([Q { k: 3 }, Q { k: 4 }])) + rd(E.B); }`, 11},
	// The safe half for the deeper kinds: a borrowed temp must never release,
	// so an element walk can never double-free one. 30 rounds, returning the
	// underflow counter's verdict through the answer.
	{"iife-strarr-no-underflow", `enum E { A(string[]), B }
function rd(e: E): string[] { return (match (e) { E.A(xs) => xs, E.B => ["z"] }); }
function round(i: i32): i32 { let k: E = E.A(["a", "b"]); return rd(k).len() + rd(E.B).len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 30) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`, 90},

	// --- controls: these lowered BEFORE the widening and must not move -------
	{"iife-array-literals-only", `function rd(i: i32): i32[] { return (match (i) { 0 => [1, 2], _ => [3] }); }
function main(): i32 { return rd(0).len() + rd(1).len(); }`, 3},
	{"iife-array-if-expr", `function rd(i: i32): i32[] { return (if (i == 0) { [1, 2] } else { [3] }); }
function main(): i32 { return rd(0).len() + rd(1).len(); }`, 3},
	{"iife-array-param-arms", `function rd(i: i32, xs: i32[]): i32[] { return (match (i) { 0 => xs, _ => xs }); }
function main(): i32 { return rd(0, [1, 2]).len(); }`, 2},
	{"iife-i32-payload", `enum E { A(i32), B }
function rd(e: E): i32 { return (match (e) { E.A(n) => n, E.B => 7 }); }
function main(): i32 { return rd(E.A(2)) + rd(E.B); }`, 9},
	{"iife-string-payload", `enum E { A(string), B }
function rd(e: E): string { return (match (e) { E.A(s) => s, E.B => "xy" }); }
function main(): i32 { return rd(E.A("abc")).len() + rd(E.B).len(); }`, 5},
	{"stmt-form-array-payload", `enum E { A(i32[]), B }
function rd(e: E, i: i32): i32[] { match (e) { E.A(xs) => { return xs; }, E.B => { return [i]; } } }
function main(): i32 { return rd(E.A([1, 2]), 0).len() + rd(E.B, 5).len(); }`, 3},
}

// TestSelfHostIifeArrayResultIRX86_64 — the x86-64 IR path.
func TestSelfHostIifeArrayResultIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range iifeArrayResultCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostIifeArrayResultIRArm64 — the arm64 IR path, where the self-host
// produces the finished binary itself (emit + assemble + link in-process).
func TestSelfHostIifeArrayResultIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range iifeArrayResultCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
