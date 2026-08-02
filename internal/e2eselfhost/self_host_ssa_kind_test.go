package e2eselfhost

import (
	"os/exec"
	"testing"
)

// TestSelfHostSSAKindRegistry exercises the self-hosted SSA layer's integer
// kind registry (examples/self_host/ssa.fern's kind_id / kind_name /
// kind_count for SInst and term_id / term_name / term_count for STerm,
// issue #4394 lever 2 / #5351 — the string->int kind conversion foundation,
// mirroring ir.fern's landed op-kind registry).
//
// The ssa_kind_run driver walks both registries and asserts the id<->name
// bijections and both sentinel contracts (SInst: unknown -> 0 / 0 and
// out-of-range -> "invalid"; STerm: the t_none empty-string kind <-> tag 0),
// printing a deterministic report and exiting with the failure count. A kind
// added to only one direction, or a duplicate id, fails the golden here
// rather than silently misdispatching a backend once the tag is wired.
func TestSelfHostSSAKindRegistry(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("ssa_kind_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssa_kind_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "ssa_kind_run.fern", "ssa_kind_run")

	const want = "kind_count=27\n" +
		"bijection_ok=27\n" +
		"bijection_failures=0\n" +
		"unknown_id=0\n" +
		"id0=invalid\n" +
		"oob=invalid\n" +
		"const_int=1\n" +
		"binary=9\n" +
		"phi=8\n" +
		"store_elem=16\n" +
		"write_file=27\n" +
		"term_count=3\n" +
		"term_bijection_ok=3\n" +
		"term_bijection_failures=0\n" +
		"term_none_id=0\n" +
		"term_none_name_len=0\n" +
		"ret=1\n" +
		"brif=3\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("ssa_kind_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("SSA kind registry report mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("ssa_kind_run exit code = %d, want 0 (bijection failures)", code)
	}
}
