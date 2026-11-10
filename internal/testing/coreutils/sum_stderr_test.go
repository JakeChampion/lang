package coreutils

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSumBytesRejectsUnexpectedStderr(t *testing.T) {
	if os.Getenv("FERN_SUM_STDERR_PROBE") == "1" {
		oracle := crossArgv(referenceBin(t, "sum"))
		shell, err := exec.LookPath("sh")
		if err != nil {
			t.Fatal(err)
		}
		noisy := []string{shell, "-c", `printf 'unexpected sum diagnostic\n' >&2; exec "$@"`, "sum-noisy"}
		noisy = append(noisy, oracle[:len(oracle)-1]...)
		e2eharness.RunSumByteCasesAgainst(t, oracle[len(oracle)-1], noisy, oracle, nil)
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestSumBytesRejectsUnexpectedStderr$", "-test.v")
	cmd.Env = append(os.Environ(), "FERN_SUM_STDERR_PROBE=1")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "stderr differs:") {
		t.Fatalf("the byte harness did not reject unexpected stderr: %v\n%s", err, out)
	}
}
