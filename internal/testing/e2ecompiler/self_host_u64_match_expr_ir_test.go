package e2ecompiler

import (
	"os/exec"
	"testing"
)

var u64MatchExprCases = []struct {
	name string
	src  string
	exit int
}{
	{"bare-return", `function f(a: u64, b: u64): u64 { return (match (a *? b) { Some(v) => v, None => 7u64 }); }
function main(): i32 { return (f(10u64, 3u64) as i32) & 63; }`, 30},
	{"operand-add", `function f(o: Option[u64], b: u64): u64 { return (match (o) { Some(v) => v, None => 7u64 }) + b; }
function main(): i32 { return (f(Some(10u64), 3u64) as i32) & 63; }`, 13},
	{"operand-div", `function f(a: u64, b: u64): u64 { return (match (a *? b) { Some(v) => v, None => 7u64 }) / b; }
function main(): i32 { return (f(10u64, 3u64) as i32) & 63; }`, 10},
	{"rhs-operand", `function f(a: u64, b: u64): u64 { return a / (match (a *? b) { Some(v) => v, None => 7u64 }); }
function main(): i32 { return (f(30u64, 3u64) as i32) & 63; }`, 0},
	{"var-bound", `function f(a: u64, b: u64): u64 { let c: u64 = (match (a *? b) { Some(v) => v, None => 7u64 }); return c + b; }
function main(): i32 { return (f(10u64, 3u64) as i32) & 63; }`, 33},
	{"unsigned-div", `function f(o: Option[u64]): u64 { return (match (o) { Some(v) => v, None => 7u64 }) / 2u64; }
function main(): i32 { return (f(Some(18446744073709551615u64)) as i32) & 63; }`, 63},
	{"i64-control", `function f(a: i64, b: i64): i64 { return (match (a *? b) { Some(v) => v, None => 7i64 }) / b; }
function main(): i32 { return (f(10i64, 3i64) as i32) & 63; }`, 10},
	{"i32-control", `function f(a: i32, b: i32): i32 { return (match (a *? b) { Some(v) => v, None => 7i32 }) / b; }
function main(): i32 { return f(10, 3) & 63; }`, 10},
}

func TestSelfHostU64MatchExprIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range u64MatchExprCases {
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

func TestSelfHostU64MatchExprIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range u64MatchExprCases {
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
