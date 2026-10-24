package e2ecompiler

import (
	"os/exec"
	"testing"
)

const strEqLengthSweepSrc = `function rep(c: string, n: i32): string {
    let s: string = "";
    let i: i32 = 0;
    while (i < n) { s = s + c; i = i + 1; }
    return s;
}

function main(): i32 {
    let n: i32 = 0;
    while (n <= 24) {
        let a: string = rep("a", n);
        if (a.len() != n) { return 200; }
        if (a != rep("a", n)) { return 60 + n; }
        if (a == rep("a", n + 1)) { return 100 + n; }
        let k: i32 = 0;
        while (k < n) {
            let b: string = rep("a", k) + "b" + rep("a", n - k - 1);
            if (b.len() != n) { return 201; }
            if (a == b) { return 150 + k; }
            k = k + 1;
        }
        n = n + 1;
    }
    return 42;
}
`

func strEqLengthSweepWant(t *testing.T, got int) {
	t.Helper()
	switch {
	case got == 42:
	case got == 200 || got == 201:
		t.Errorf("exited %d — `rep` built the wrong length, so the sweep tested nothing", got)
	case got >= 60 && got <= 84:
		t.Errorf("exited %d — two equal strings of length %d compared unequal", got, got-60)
	case got >= 100 && got <= 124:
		t.Errorf("exited %d — length %d compared equal to length %d", got, got-100, got-100+1)
	case got >= 150 && got <= 174:
		t.Errorf("exited %d — a difference at byte %d was not seen", got, got-150)
	default:
		t.Errorf("exited %d, want 42", got)
	}
}

func TestSelfHostStrEqLengthSweepX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := string(runCapture(t, gcc, runner, driverBin, []byte(strEqLengthSweepSrc)))
	if len(asm) == 0 {
		t.Fatal("self-host emitted 0 bytes")
	}
	bin := buildBin(t, gcc, dir, "streqlen", asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	_ = cmd.Run()
	strEqLengthSweepWant(t, cmd.ProcessState.ExitCode())
}

func TestSelfHostStrEqLengthSweepArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(strEqLengthSweepSrc), "-target", "arm64-linux"))
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "streqlen_arm64", asm)
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	strEqLengthSweepWant(t, cmd.ProcessState.ExitCode())
}
