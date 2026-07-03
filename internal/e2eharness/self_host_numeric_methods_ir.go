// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/self_host_numeric_methods_ir_test.go.
package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// InterpExit runs `fern -interp` on src (written to a temp file) and returns the
// reference exit code — the oracle for the self-host comparisons below.
func InterpExit(t *testing.T, interpBin, src string) int {
	t.Helper()
	f := filepath.Join(t.TempDir(), "oracle.fern")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatalf("write oracle src: %v", err)
	}
	cmd := exec.Command(interpBin, "-interp", f)
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode()
}
