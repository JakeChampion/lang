package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// allocTrapSrc allocates without bound (string concat reallocates the
// whole string each iteration, so cumulative allocation grows
// quadratically and blows past the 1 GiB bump heap). The self-host's
// __fern_alloc bounds check must trap with a clean, recognisable exit
// code (137) rather than silently running past the heap into adjacent
// .bss (the strbuf output accumulator) and corrupting it.
const allocTrapSrc = "function main(): i32 { var s: string = \"\"; var i: i32 = 0; while (i < 60000) { s = s + \"x\"; i = i + 1; } return s.len(); }"

// TestSelfHostAllocTrapX86_64 — heap-overflow trap, self-hosted x86-64.
func TestSelfHostAllocTrapX86_64(t *testing.T) {
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

	asm := runCapture(t, gcc, runner, driverBin, []byte(allocTrapSrc))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "alloc_trap", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 137 {
		t.Errorf("heap-overflow program exited %d, want 137 (clean OOM trap)", code)
	}
}

// TestSelfHostAllocTrapArm64 — CI-gated arm64 counterpart.
func TestSelfHostAllocTrapArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "asm_arm64.fern", "asm_arm64_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_run.fern", "driver")

	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(allocTrapSrc))
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	progBin := buildBin(t, arm64gcc, dir, "alloc_trap", string(asm))
	cmd := runArm64Bin(qemu, progBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 137 {
		t.Errorf("heap-overflow program exited %d, want 137 (clean OOM trap)", code)
	}
}
