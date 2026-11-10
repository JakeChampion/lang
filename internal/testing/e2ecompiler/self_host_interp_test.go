package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"testing"
)

func writeSelfHostInterpProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/interp_check_run.fern")
	return dir
}

func TestSelfHostInterpX86_64(t *testing.T) {
	runner := x86_64Runner(t)
	dir := writeSelfHostInterpProject(t)
	binPath := buildSelfHostBinFor(t, dir, "drivers/interp_check_run.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	out, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("interp self-checks exit %d:\n%s", code, out)
	}
}

func TestSelfHostInterpArm64(t *testing.T) {
	qemu := arm64Runner(t)
	dir := writeSelfHostInterpProject(t)
	binPath := buildSelfHostBinFor(t, dir, "drivers/interp_check_run.fern", "prog", e2eharness.TargetArm64Linux)
	cmd := runArm64Bin(qemu, binPath)
	out, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("interp self-checks exit %d:\n%s", code, out)
	}
}
