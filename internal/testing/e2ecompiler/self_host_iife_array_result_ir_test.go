package e2ecompiler

import (
	"os/exec"
	"testing"
)

var iifeArrayResultCases = []struct {
	name string
	src  string
	exit int
}{
	{"iife-array-payload-mixed", `enum E { A(i32[]), B }
function rd(e: E, i: i32): i32[] { return (match (e) { E.A(xs) => xs, E.B => [i] }); }
function main(): i32 { return rd(E.A([1, 2]), 0).len() + rd(E.B, 5).len(); }`, 3},

	{"iife-array-payload-f64", `enum E { A(f64[]), B }
function rd(e: E): f64[] { return (match (e) { E.A(xs) => xs, E.B => [1.5] }); }
function main(): i32 { return rd(E.A([1.5, 2.5])).len() + rd(E.B).len(); }`, 3},
	{"iife-array-payload-i64", `enum E { A(i64[]), B }
function rd(e: E): i64[] { return (match (e) { E.A(xs) => xs, E.B => [1i64] }); }
function main(): i32 { return rd(E.A([1i64, 2i64])).len() + rd(E.B).len(); }`, 3},

	{"iife-array-bound-and-read", `enum E { A(i32[]), B }
function rd(e: E, i: i32): i32 { let v: i32[] = (match (e) { E.A(xs) => xs, E.B => [i] }); return v[0] + v.len(); }
function main(): i32 { return rd(E.A([4, 5]), 0) + rd(E.B, 6); }`, 13},

	{"iife-array-no-underflow", `enum E { A(i32[]), B }
function rd(e: E, i: i32): i32[] { return (match (e) { E.A(xs) => xs, E.B => [i] }); }
function main(): i32 {
    let i: i32 = 0;
    while (i < 30) { let k: E = E.A([i, i + 1]); if (rd(k, i).len() != 2) { return 90; } if (rd(E.B, i).len() != 1) { return 91; } i = i + 1; }
    return __rc_underflow_count();
}`, 0},

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
	{"iife-strarr-no-underflow", `enum E { A(string[]), B }
function rd(e: E): string[] { return (match (e) { E.A(xs) => xs, E.B => ["z"] }); }
function round(i: i32): i32 { let k: E = E.A(["a", "b"]); return rd(k).len() + rd(E.B).len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 30) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`, 90},

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
