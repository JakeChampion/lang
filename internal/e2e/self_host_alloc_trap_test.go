package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// allocTrapSrc allocates without bound: each iteration grows `s` by concat and
// STORES the grown string into the array `a`, so every intermediate stays live
// (cumulative allocation grows quadratically and blows past the 2.5 GiB bump
// heap). The self-host's __fern_alloc bounds check must trap with a clean,
// recognisable exit code (137) rather than silently running past the heap into
// adjacent .bss (the strbuf output accumulator) and corrupting it.
//
// The `a.append(s)` is essential: it makes `s` ESCAPE, so `s` is NOT a
// reclaimable string-builder accumulator (#2649) — without it, the consume-rebind
// reclaim frees each superseded box and the program stays flat (~1.2 GiB) and
// COMPLETES (return 100000 → exit 160) instead of trapping. Storing every
// intermediate keeps them all live, so the heap genuinely overflows.
//
// 100000 iterations is ~4.66 GiB cumulative string bytes (n²/2), robustly past
// the 2.5 GiB heap (and any heap ≤ 4 GiB) so it traps mid-loop on every backend.
const allocTrapSrc = "function main(): i32 { var a: string[] = []; var s: string = \"\"; var i: i32 = 0; while (i < 100000) { s = s + \"x\"; a.append(s); i = i + 1; } return a.len(); }"

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
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm_arm64_run.fern"} {
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
