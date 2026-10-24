package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"testing"
)

func writeSelfHostPrinterProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "printer.fern")
	return dir
}

func TestSelfHostPrinterX86_64(t *testing.T) {
	runner := x86_64Runner(t)
	dir := writeSelfHostPrinterProject(t)
	binPath := buildSelfHostBinFor(t, dir, "printer.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port printer assertion %d failed", code)
	}
}

func TestSelfHostPrinterArm64(t *testing.T) {
	qemu := arm64Runner(t)
	dir := writeSelfHostPrinterProject(t)
	binPath := buildSelfHostBinFor(t, dir, "printer.fern", "prog", e2eharness.TargetArm64Linux)
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port printer assertion %d failed", code)
	}
}
