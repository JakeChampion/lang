package main

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// buildFernForStdoutTest builds this package, with the current self-host
// compiler beside it, for tests that drive the CLI as a subprocess.
func buildFernForStdoutTest(t *testing.T) string {
	t.Helper()
	return e2eharness.FernCLI(t)
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
