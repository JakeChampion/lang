package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostIRNeedDetectionX86_64 guards the runtime-need detection (#3425):
// whether the module allocates is read off the ops of the one lowering the emit
// already performs, not off extra whole-module lowering passes, which on the
// self-host compiler held enough ops to exhaust the heap.
//
// The behavioural contract: a program that allocates pulls in the allocator and
// RC runtime and no more. If the need-marking missed it, the emitted asm would
// reference an undefined __fern_alloc and fail to link — so a successful link +
// correct exit code proves the need was marked, and the size bound that nothing
// else came with it.
func TestSelfHostIRNeedDetectionX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range []struct {
		name string
		prog string
		want int
	}{
		// Heap-only (array allocation): the allocator/RC runtime is pulled in by
		// the op_allocates marking.
		{"array-only", `function main(): i32 {
	let xs: i32[] = [10, 20, 30];
	let s: i32 = 0;
	let i: i32 = 0;
	while (i < xs.len()) { s = s + xs[i]; i = i + 1; }
	return s;
}`, 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.prog))
			if len(asm) == 0 {
				t.Fatalf("driver produced no asm")
			}
			// A generous bound confirms the per-need gating held.
			if len(asm) > 33000 {
				t.Fatalf("asm is %d bytes — expected the compact IR runtime; the need gating pulled in more than the module uses", len(asm))
			}
			progBin := buildBin(t, gcc, dir, "need_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exit %d, want %d", code, tc.want)
			}
		})
	}
}
