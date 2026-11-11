package coreutils

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestDDBytesRejectsReferenceStderr(t *testing.T) {
	if os.Getenv("FERN_DD_REFERENCE_STDERR_PROBE") == "1" {
		oracle := crossArgv(referenceBin(t, "dd"))
		noisy := append([]string{"sh", "-c", `printf 'unexpected dd reference diagnostic\n' >&2; exec "$@"`, "dd-noisy"}, oracle...)
		e2eharness.RunDDByteCasesAgainst(t, oracle[len(oracle)-1], oracle[:len(oracle)-1], noisy, nil)
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestDDBytesRejectsReferenceStderr$", "-test.v")
	cmd.Env = append(os.Environ(), "FERN_DD_REFERENCE_STDERR_PROBE=1")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "unexpected GNU diagnostic:") {
		t.Fatalf("the byte harness did not reject reference stderr: %v\n%s", err, out)
	}
}
