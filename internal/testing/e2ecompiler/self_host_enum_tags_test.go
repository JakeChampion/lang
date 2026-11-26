package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostEnumTagsContiguous(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("enum_tags_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/enum_tags_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/enum_tags_run.fern", "enum_tags_run")

	const want = "gap before: E\n" +
		"grouped: E$A E$B E$C E$A X F$P F$Q\n" +
		"gap after: (none)\n" +
		"again: E$A E$B E$C E$A X F$P F$Q\n" +
		"E$A -> 0\n" +
		"E$B -> 1\n" +
		"E$C -> 2\n" +
		"X -> 4\n" +
		"F$P -> 5\n" +
		"F$Q -> 6\n" +
		"kept: X E$A E$B\n" +
		"builtin gap: (none)\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("enum_tags_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("enum tags mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
