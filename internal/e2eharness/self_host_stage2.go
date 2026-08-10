// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/self_host_stage2_test.go.
package e2eharness

import (
	"bytes"
	"os/exec"
	"testing"
)

// RunCapture runs bin (under the qemu runner if set), feeding stdin,
// and returns stdout.
// RunCapture runs the built driver binary, pipes stdin in, and returns its
// stdout. extraArgs are appended after the binary — the arm64/darwin consumers
// pass "-target", "arm64-linux" (etc.) to select the emit backend of the folded
// asm_run driver (#4398 part 1); x86 callers pass nothing and stay unchanged.
func RunCapture(t *testing.T, gcc string, runner []string, bin string, stdin []byte, extraArgs ...string) []byte {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin, extraArgs...)
	} else {
		args := append([]string{}, runner[1:]...)
		args = append(args, bin)
		args = append(args, extraArgs...)
		cmd = exec.Command(runner[0], args...)
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run %s: %v", bin, err)
	}
	return out
}
