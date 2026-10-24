package e2ecompiler

import (
	"os/exec"
	"testing"
)

const arrayMain = "import \"std/array\";\n" +
	"function main(): i32 { let xs: i32[] = [12, 18, 24]; match (xs.gcd_all()) { Some(g) => { return g; }, None => { return 0; } } return 0; }\n"

func TestSelfHostArrayX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	asm, progDir := compileSourceModload(t, runner, driverBin, arrayMain)
	progBin := buildBin(t, gcc, progDir, "arrprog", asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 6 {
		t.Errorf("gcd_all([12,18,24]) exited %d, want 6", code)
	}
}

func TestSelfHostArrayArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)
	asm, progDir := compileSourceModload(t, x86runner, driverBin, arrayMain, "-target", "arm64-linux")
	progBin := buildBin(t, arm64gcc, progDir, "arrprog", asm)
	cmd := runArm64Bin(qemu, progBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 6 {
		t.Errorf("gcd_all([12,18,24]) exited %d, want 6", code)
	}
}
