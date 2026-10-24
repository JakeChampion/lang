package e2ecompiler

import (
	"os/exec"
	"testing"
)

var valueBlockElemWidthCases = []struct {
	name string
	src  string
	exit int
}{
	{"call-arm-in-i64-array-elem", `function id[T](x: T): T { return x; } function main(): i32 { let xs: i64[] = [(match ((366i64 ^ 456i64) +? 673i64) { Some(v) => v, None => id((if (true) { 1099511628488i64 } else { 5i64 })) })]; return ((xs[0i32] as i32) & 63i32); }`, 7},
	{"call-arm-ifexpr-i64-array-elem", `function id[T](x: T): T { return x; } function main(): i32 { let xs: i64[] = [(if (true) { id(1099511628488i64) } else { 5i64 })]; return ((xs[0i32] as i32) & 63i32); }`, 8},
	{"literal-arm-unchanged", `function main(): i32 { let xs: i64[] = [(match ((366i64 ^ 456i64) +? 673i64) { Some(v) => v, None => 0i64 })]; return ((xs[0i32] as i32) & 63i32); }`, 7},
	{"scalar-binding-unchanged", `function main(): i32 { let w: i64 = (match ((366i64 ^ 456i64) +? 673i64) { Some(v) => v, None => 0i64 }); return ((w as i32) & 63i32); }`, 7},
	{"i32-array-elem-unchanged", `function main(): i32 { let xs: i32[] = [(if (true) { 7i32 } else { 5i32 })]; return (xs[0i32] & 63i32); }`, 7},
	{"none-arm-option-binding", `function main(): i32 { let v: Option[i32] = (if (true) { None } else { Some(3i32) }); return match (v) { Some(x) => x, None => 7i32 }; }`, 7},
}

func TestSelfHostValueBlockElemWidthIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range valueBlockElemWidthCases {
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

func TestSelfHostValueBlockElemWidthIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range valueBlockElemWidthCases {
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
