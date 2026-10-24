package e2eharness

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func RunCapture(t *testing.T, gcc string, runner []string, bin string, stdin []byte, extraArgs ...string) []byte {
	t.Helper()
	cmd := RunX86_64Bin(runner, bin, extraArgs...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("run %s: %v\n%s", bin, err, stderr)
	}
	return out
}

func RunCaptureStrictIR(t *testing.T, gcc string, runner []string, bin string, stdin []byte, extraArgs ...string) []byte {
	t.Helper()
	cmd := RunX86_64Bin(runner, bin, extraArgs...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
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
