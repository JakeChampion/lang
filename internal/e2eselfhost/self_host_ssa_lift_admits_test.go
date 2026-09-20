package e2eselfhost

import (
	"os/exec"
	"testing"
)

// TestSelfHostSSALiftAdmitsEveryOp pins the production lift's admission
// census (examples/self_host/ssa_lift_admits_run.fern): every registered IR
// op kind reaches the register path, as an instruction of the lift's own or
// through the stack machine's arm for it, except the ones listed here: a
// kind with a pop count ir.op_pops does not model cannot be bridged. A new
// line in the report is a kind the register path declines on every program
// that uses it, which is what this gate exists to refuse.
func TestSelfHostSSALiftAdmitsEveryOp(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("ssa_lift_admits_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssa_lift_admits_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "ssa_lift_admits_run.fern", "ssa_lift_admits_run")

	// load, store and call_closure_direct are registered kinds no lowering
	// produces and no backend has an arm for; they stay unmodelled in
	// op_pops, so the lift cannot bridge them either.
	const want = "load pops=-1 pushes=1\n" +
		"store pops=-1 pushes=1\n" +
		"call_closure_direct pops=-1 pushes=1\n" +
		"registered=306 declined=3\n"

	cmd := exec.Command(bin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ssa_lift_admits_run: %v\n%s", err, out)
	}
	if got := string(out); got != want {
		t.Errorf("lift admission census mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
