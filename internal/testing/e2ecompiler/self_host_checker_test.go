package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"testing"
)

func writeSelfHostCheckerProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "checker.fern")
	return dir
}

func TestSelfHostCheckerX86_64(t *testing.T) {
	runner := x86_64Runner(t)
	dir := writeSelfHostCheckerProject(t)
	binPath := buildSelfHostBinFor(t, dir, "checker.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port checker assertion %d failed", code)
	}
}

func TestSelfHostCheckerArm64(t *testing.T) {
	qemu := arm64Runner(t)
	dir := writeSelfHostCheckerProject(t)
	binPath := buildSelfHostBinFor(t, dir, "checker.fern", "prog", e2eharness.TargetArm64Linux)
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port checker assertion %d failed", code)
	}
}
