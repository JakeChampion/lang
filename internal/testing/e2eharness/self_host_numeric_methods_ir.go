package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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
