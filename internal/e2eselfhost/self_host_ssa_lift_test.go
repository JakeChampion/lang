package e2eselfhost

import (
	"os/exec"
	"testing"
)

// TestSelfHostSSALift exercises the self-hosted stack-IR -> SSA lift
// (examples/self_host/ssa_lift.fern, slices 0+1 — see
// docs/SELFHOST-SSA-ALWAYS.md). The ssa_lift_run driver hand-builds a few
// ir.Op[] streams, lifts each into an ssa.SFunc, and RUNS the lifted SSA
// through ssa.eval_func, validating value flow on both arms of the branch
// cases and across loop iterations (so the if-merge AND loop-header phi
// reconstruction is exercised, not just type-checked). This
// pins the lift end-to-end through the self-host -> native pipeline, proving
// ssa_lift.fern compiles (not just type-checks) and that the lifted SSA is
// executable with the right semantics.
//
// The driver is built natively via the Go x86-64 backend; its stdout is the
// per-check TAP-style report and its exit code is the number of failed checks
// (0 == all green).
func TestSelfHostSSALift(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("ssa_lift_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssa_lift_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "ssa_lift_run.fern", "ssa_lift_run")

	// Golden report — locks the lift's value flow (both branch arms), the
	// phi count, the call_direct structural check, and the out-of-subset bail.
	const want = "ok - add(3,4) => 7\n" +
		"ok - add(10,32) => 42\n" +
		"ok - ifelse c=1 => 11\n" +
		"ok - ifelse c=0 => 22\n" +
		"ok - ifelse builds exactly one phi\n" +
		"ok - if-noelse c=1 => 7\n" +
		"ok - if-noelse c=0 => 100\n" +
		"ok - call_direct lifts to one call inst\n" +
		"ok - while-sum n=5 => 15\n" +
		"ok - while-sum n=1 => 1\n" +
		"ok - while-sum n=0 => 0\n" +
		"ok - evensum n=6 => 6\n" +
		"ok - evensum n=7 => 12\n" +
		"ok - evensum n=1 => 0\n" +
		"ok - break n=10 => 3\n" +
		"ok - break n=2 => 1\n" +
		"ok - continue n=5 => 12\n" +
		"ok - continue n=4 => 7\n" +
		"ok - out-of-subset op bails\n" +
		"# all lift checks passed\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("ssa_lift_run did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("ssa_lift_run exit = %d, want 0\noutput:\n%s", code, out)
	}
	if got := string(out); got != want {
		t.Fatalf("ssa_lift_run output mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}
