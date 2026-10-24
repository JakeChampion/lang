package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"testing"
)

func writeSelfHostParserProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "parser.fern")
	return dir
}

func TestSelfHostParserX86_64(t *testing.T) {
	runner := x86_64Runner(t)
	dir := writeSelfHostParserProject(t)
	binPath := buildSelfHostBinFor(t, dir, "parser.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port parser assertion %d failed", code)
	}
}

func TestSelfHostParserArm64(t *testing.T) {
	qemu := arm64Runner(t)
	dir := writeSelfHostParserProject(t)
	binPath := buildSelfHostBinFor(t, dir, "parser.fern", "prog", e2eharness.TargetArm64Linux)
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port parser assertion %d failed", code)
	}
}
