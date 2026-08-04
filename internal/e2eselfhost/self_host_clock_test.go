package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostClockX86_64 exercises the self-hosted x86-64 emitter's
// `now_unix_ms` and `monotonic_ns` clock builtins (std/test plan: the
// time batch). Both return an i64 in %rax via clock_gettime(2).
//
// now_unix_ms: assert it sits well past 1e12 ms (~2001-09), proving a
// real wall-clock value round-trips through the 64-bit slot (the value
// exceeds 32-bit range, so a truncating path would fail this).
//
// monotonic_ns: take two stamps and assert the second is >= the first,
// proving the clock advances monotonically.
func TestSelfHostClockX86_64(t *testing.T) {
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

	prog := `function main(): i32 {
    if (now_unix_ms() <= 1000000000000) { return 1; }
    var a: i64 = monotonic_ns();
    var b: i64 = monotonic_ns();
    if (b < a) { return 2; }
    return 7;
}`
	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "clock", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("clock program exited %d, want 7 (1=now_unix_ms too small, 2=monotonic regressed)", code)
	}
}

// TestSelfHostClockArm64 is the ARM64 counterpart: the self-hosted
// ARM64 emitter's now_unix_ms / monotonic_ns. The asm_ir_run (-target arm64) driver
// (an x86 host binary) compiles the same clock program to aarch64 asm;
// the assembled binary runs under qemu-aarch64 (which passes
// clock_gettime through to the host) and must exit 7.
func TestSelfHostClockArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	prog, _, err := modload.Load(filepath.Join(dir, "asm_ir_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverBin := buildBin(t, x86gcc, dir, "driver", asm)

	clockSrc := `function main(): i32 {
    if (now_unix_ms() <= 1000000000000) { return 1; }
    var a: i64 = monotonic_ns();
    var b: i64 = monotonic_ns();
    if (b < a) { return 2; }
    return 7;
}`
	clockAsm := runCapture(t, x86gcc, x86runner, driverBin, []byte(clockSrc), "-target", "arm64")
	if len(clockAsm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes for the clock program")
	}
	clockBin := buildBin(t, arm64gcc, dir, "clock", string(clockAsm))

	cmd := runArm64Bin(qemu, clockBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("clock program exited %d, want 7 (1=now_unix_ms too small, 2=monotonic regressed)", code)
	}
}
