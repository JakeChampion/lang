package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"testing"
)

func TestSelfHostLiterateX86_64(t *testing.T) {
	runner := x86_64Runner(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "literate.fern")
	binPath := buildSelfHostBinFor(t, dir, "literate.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port literate assertion %d failed", code)
	}
}

func TestSelfHostLiterateArm64(t *testing.T) {
	qemu := arm64Runner(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "literate.fern")
	binPath := buildSelfHostBinFor(t, dir, "literate.fern", "prog", e2eharness.TargetArm64Linux)
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port literate assertion %d failed", code)
	}
}

func TestSelfHostLiterateWASM(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "literate.fern")
	core := buildSelfHostBinFor(t, dir, "literate.fern", "prog.wasm", e2eharness.TargetWasm32Wasi)
	cmd := e2eharness.RunWasmCore(t, core)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port literate assertion %d failed", code)
	}
}
