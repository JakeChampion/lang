package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// allocTrapSrc drives the bump cursor past the arena end with raw `__alloc`
// calls. The self-host's __fern_alloc bounds check must trap with a clean,
// recognisable exit code (125, ExitArenaExhausted) rather than silently running
// past the heap into
// adjacent .bss (the strbuf output accumulator) and corrupting it.
//
// Why raw `__alloc` and not cumulative string growth: `__alloc(n)` returns a
// fresh n-byte block and NEVER writes it (no zero-init, and its `usize` result
// is a scalar — no rc, so no reclaim ever recycles it). Each call advances the
// bump CURSOR by ~2 GiB (pow2-rounded) while touching ZERO physical pages, so
// residency stays flat at a few MB no matter how far the cursor races. ~8 calls
// overrun the 16 GiB arena and the ~9th trips the bounds check → exit(125) in
// ~1 ms.
//
// This replaced the old approach (grow `s` by concat, `a.append(s)` to keep
// every intermediate live so ~10.5 GiB of string bytes overflowed the arena):
// once the arena reached 16 GiB (#5218) that no longer worked on a 16 GB CI
// runner. Touching ~16 GiB to reach the arena wall host-OOMs FIRST — a SIGKILL,
// which Go reports as ExitCode -1, not the clean 125 this asserts — and any
// count small enough to fit RAM stays UNDER the 16 GiB arena and COMPLETES
// (exit 0). Decoupling cursor advance from residency removes that dependency on
// arena-size ≈ runner-RAM entirely, and the loop bound (100000 × ~2 GiB, far
// past any arena) also makes the trap immune to future arena bumps — it still
// fires after ~8 iterations, the rest are unreached.
const allocTrapSrc = "function main(): i32 { var i: i32 = 0; var last: usize = 0; while (i < 100000) { last = __alloc(2000000000); i = i + 1; } if (last == 0) { return 99; } return i; }"

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
	if code := cmd.ProcessState.ExitCode(); code != 125 {
		t.Errorf("heap-overflow program exited %d, want 125 (clean arena trap; 137 "+
			"would mean the HOST OOM-killed it, a different failure)", code)
	}
}

// TestSelfHostAllocTrapArm64 — CI-gated arm64 counterpart.
func TestSelfHostAllocTrapArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(allocTrapSrc), "-target", "arm64-linux")
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	progBin := buildBin(t, arm64gcc, dir, "alloc_trap", string(asm))
	cmd := runArm64Bin(qemu, progBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 125 {
		t.Errorf("heap-overflow program exited %d, want 125 (clean arena trap; 137 "+
			"would mean the HOST OOM-killed it, a different failure)", code)
	}
}
