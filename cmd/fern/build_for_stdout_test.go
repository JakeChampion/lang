package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildFernForStdoutTest builds this package into a temp dir and returns the
// binary, for tests that drive the CLI as a subprocess.
func buildFernForStdoutTest(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fern")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, o)
	}
	return bin
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
