package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostCapsInventory exercises the self-host's package-capability
// inventory (examples/self_host/caps.fern, #6634 slice 1) — the port of
// native's internal/caps table, and the second of the two independent
// capability systems CLAUDE.md requires a new builtin to be classified in.
//
// internal/caps/selfhost_parity_test.go compares the table with native's by
// reading it as data, which is the cheap gate. This one COMPILES it, so the
// module cannot rot into a file nothing builds, and asserts the internal
// properties a diff against native cannot see: that every tag names a word in
// the vocabulary, that the two halves do not overlap, and that a name in
// neither half reads as unclassified rather than as permitted.
func TestSelfHostCapsInventory(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("caps_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "caps_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "caps_run.fern", "caps_run")

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("caps_run did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("caps_run exit code = %d, want 0 — that code is the failing assertion's id in caps_run.fern", code)
	}
	if want := "caps: the package-capability inventory is consistent"; !strings.Contains(string(out), want) {
		t.Errorf("caps_run stdout = %q, want it to contain %q", out, want)
	}
}
