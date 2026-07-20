package e2eselfhost

import (
	"os/exec"
	"testing"
)

// arrayMain exercises std/array's gcd_all (which needs i32.gcd) on a
// program importing ./array (+ ./sort for the sort_* funcs it refs).
const arrayMain = "import \"std/array\";\n" +
	"function main(): i32 { var xs: i32[] = [12, 18, 24]; match (xs.gcd_all()) { Some(g) => { return g; }, None => { return 0; } } return 0; }\n"

// TestSelfHostArrayX86_64 — the self-hosted compiler compiles real
// std/array (needed i32.gcd/lcm); gcd_all([12,18,24]) == 6.
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

// TestSelfHostArrayArm64 — CI-gated arm64 counterpart.
func TestSelfHostArrayArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)
	asm, progDir := compileSourceModload(t, x86runner, driverBin, arrayMain, "-target", "arm64")
	progBin := buildBin(t, arm64gcc, progDir, "arrprog", asm)
	cmd := runArm64Bin(qemu, progBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 6 {
		t.Errorf("gcd_all([12,18,24]) exited %d, want 6", code)
	}
}
