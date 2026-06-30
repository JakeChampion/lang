package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostSSALiftEmit is the end-to-end proof of the stack-IR -> SSA LIFT
// feeding real codegen: the ssa_lift_emit_run driver builds a hand-coded
// ir.Op[] program, lifts it to SSA (ssa_lift.lift_from_ir), runs the existing
// ssa.optimize, and emits x86-64 via ssa_x86.emit_program. This test assembles
// that output (gcc -static -nostdlib -no-pie) and runs it, asserting the exit
// code equals the program's value — so the lifted SSA is proven not merely
// interpretable (eval_func) but accepted by the existing optimiser and x86-64
// backend all the way to executing native code. Each program is run twice:
// once with the default slot-addressed emit, once with -regalloc (the
// linear-scan allocator) over the lifted SSA.
func TestSelfHostSSALiftEmit(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("emitted x86-64 runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "lexer.fern", "parser.fern", "astwalk.fern",
		"ir.fern", "ssa.fern", "ssa_x86.fern", "ssa_lift.fern", "ssa_lift_emit_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	bin := buildSelfHostBin(t, x86gcc, dir, "ssa_lift_emit_run.fern", "ssa_lift_emit_run")

	emit := func(t *testing.T, args ...string) []byte {
		t.Helper()
		out, err := exec.Command(bin, args...).Output()
		if err != nil {
			t.Fatalf("emit driver failed for %v: %v", args, err)
		}
		return out
	}
	run := func(t *testing.T, asm []byte, tag string) int {
		t.Helper()
		asmPath := filepath.Join(dir, "le_"+tag+".s")
		binPath := filepath.Join(dir, "le_"+tag)
		if err := os.WriteFile(asmPath, asm, 0o644); err != nil {
			t.Fatalf("write asm: %v", err)
		}
		if out, err := exec.Command(x86gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("gcc failed: %v\n%s\n--- asm ---\n%s", err, out, asm)
		}
		cmd := exec.Command(binPath)
		_ = cmd.Run()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("emitted program did not exit normally")
		}
		return cmd.ProcessState.ExitCode()
	}

	cases := []struct {
		prog string
		want int
	}{
		{"arith", 13},     // (3+4)*2-1 — straight-line
		{"loopsum", 15},   // sum 1..5 — loop-header phis
		{"branch", 42},    // void if-merge
		{"breakloop", 42}, // break out of a loop
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.prog, func(t *testing.T) {
			if got := run(t, emit(t, tc.prog), tc.prog); got != tc.want {
				t.Errorf("lift->emit %s = %d, want %d", tc.prog, got, tc.want)
			}
		})
		t.Run(tc.prog+"-regalloc", func(t *testing.T) {
			if got := run(t, emit(t, tc.prog, "-regalloc"), tc.prog+"-ra"); got != tc.want {
				t.Errorf("lift->emit -regalloc %s = %d, want %d", tc.prog, got, tc.want)
			}
		})
	}
}
