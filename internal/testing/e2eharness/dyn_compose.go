package e2eharness

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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
