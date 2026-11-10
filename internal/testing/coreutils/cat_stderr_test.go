package coreutils

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestCatBytesRejectsUnexpectedStderr(t *testing.T) {
	if os.Getenv("FERN_CAT_STDERR_PROBE") == "1" {
		oracle := crossArgv(referenceBin(t, "cat"))
		shell, err := exec.LookPath("sh")
		if err != nil {
			t.Fatal(err)
		}
		noisy := []string{shell, "-c", `printf 'unexpected cat diagnostic\n' >&2; exec "$@"`, "cat-noisy"}
		noisy = append(noisy, oracle[:len(oracle)-1]...)
		e2eharness.RunCatByteCasesAgainst(t, oracle[len(oracle)-1], noisy, oracle, nil)
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestCatBytesRejectsUnexpectedStderr$", "-test.v")
	cmd.Env = append(os.Environ(), "FERN_CAT_STDERR_PROBE=1")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "stderr differs:") {
		t.Fatalf("the byte harness did not reject unexpected stderr: %v\n%s", err, out)
	}
}
