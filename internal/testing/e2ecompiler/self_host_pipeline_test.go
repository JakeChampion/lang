package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"testing"
)

func writeSelfHostPipelineProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "constfold.fern", "checker.fern", "interp.fern", "drivers/pipeline.fern")
	return dir
}

func TestSelfHostPipelineX86_64(t *testing.T) {
	runner := x86_64Runner(t)
	dir := writeSelfHostPipelineProject(t)
	binPath := buildSelfHostBinFor(t, dir, "drivers/pipeline.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port pipeline assertion %d failed", code)
	}
}

func TestSelfHostPipelineArm64(t *testing.T) {
	qemu := arm64Runner(t)
	dir := writeSelfHostPipelineProject(t)
	binPath := buildSelfHostBinFor(t, dir, "drivers/pipeline.fern", "prog", e2eharness.TargetArm64Linux)
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port pipeline assertion %d failed", code)
	}
}
