package e2eselfhost

import (
	"os/exec"
	"testing"
)

// TestSelfHostUtilHashBucket pins util.hash_bucket's four-byte step against
// the byte-at-a-time roll it stands for. The bucket a name lands in reaches no
// emitted byte, so a wrong weight in the step passes every behaviour test and
// the emit-hash sweep alike; the util_hash_run driver is the one gate that
// evaluates the arithmetic.
func TestSelfHostUtilHashBucket(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("util_hash_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "util_hash_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "util_hash_run.fern", "util_hash_run")

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("util_hash_run did not exit normally")
	}
	const want = "checked=1008 wrong=0\n"
	if got := string(out); got != want {
		t.Errorf("hash report mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("util_hash_run exit code = %d, want 0", code)
	}
}
