// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/self_host_stage2_test.go.
package e2eharness

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// RunCapture runs the built driver binary (under the qemu runner if set), pipes
// stdin in, and returns its stdout. extraArgs are appended after the binary —
// the arm64/darwin consumers pass "-target", "arm64" (etc.) to select the emit
// backend of the folded asm_run driver (#4398 part 1); x86 callers pass nothing
// and stay unchanged.
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

// RunCaptureStrictIR is RunCapture with FERN_STRICT_IR=1 (#5646) set on the
// driver, so a per-function IR bail refuses (exit 3, naming the site) instead of
// being absorbed by the module-level retry.
//
// Reach for it in any case whose point is that a shape STAYS on the IR path.
// Plain RunCapture cannot express that: it asserts the answer, and a bail can
// reach the same answer by another route, so the case passes on the commit the
// fix has not landed on and pins nothing (#6602). An asm-label witness does not
// separate them either — a module that bailed still emits `.Lir_*` labels for
// the functions that did lower.
func RunCaptureStrictIR(t *testing.T, gcc string, runner []string, bin string, stdin []byte, extraArgs ...string) []byte {
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
	// Drop any ambient setting before adding ours: the package can be run with
	// the flag already exported as a probe, and a duplicate key resolves to the
	// FIRST occurrence, so appending alone would leave the outer value in force.
	cmd.Env = []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "FERN_STRICT_IR=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "FERN_STRICT_IR=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("run %s under FERN_STRICT_IR=1: exited %d\n%s", filepath.Base(bin), code, stderr.String())
	}
	if stdout.Len() == 0 {
		t.Fatalf("run %s under FERN_STRICT_IR=1: emitted 0 bytes\n%s", filepath.Base(bin), stderr.String())
	}
	return stdout.Bytes()
}
