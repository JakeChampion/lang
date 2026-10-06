// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/testing/e2e and
// internal/testing/e2ecompiler (#4398 part 3). Extracted verbatim from
// internal/testing/e2e/dyn_compose_test.go.
package e2eharness

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// RunInterp runs src under the interpreter (`fern -interp`) and returns its
// stdout and exit code: the oracle a self-host answer is compared against
// (docs/NATIVE-CONVERGENCE.md §3).
func RunInterp(t testing.TB, src string) (stdout string, exitCode int) {
	t.Helper()
	bin := BuildLangBinForInterp(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", p)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("interp did not run\nstderr: %s", errb.String())
	}
	return out.String(), cmd.ProcessState.ExitCode()
}

func RunInterpExit(t *testing.T, src string) int {
	t.Helper()
	_, code := RunInterp(t, src)
	return code
}
