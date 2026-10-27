package e2ecompiler

import (
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func writeSelfHostConstfoldProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "constfold.fern")
	return dir
}

func TestSelfHostConstfoldX86_64(t *testing.T) {
	runner := x86_64Runner(t)
	dir := writeSelfHostConstfoldProject(t)
	binPath := buildSelfHostBinFor(t, dir, "constfold.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port constfold assertion %d failed", code)
	}
}

func TestSelfHostConstfoldArm64(t *testing.T) {
	qemu := arm64Runner(t)
	dir := writeSelfHostConstfoldProject(t)
	binPath := buildSelfHostBinFor(t, dir, "constfold.fern", "prog", e2eharness.TargetArm64Linux)
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port constfold assertion %d failed", code)
	}
}

func TestSelfHostConstfoldWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := writeSelfHostConstfoldProject(t)
	bin := buildSelfHostBinFor(t, dir, "constfold.fern", "constfold.wasm", e2eharness.TargetWasm32Wasi)
	if out, err := exec.Command(wasmtime, "run", bin).CombinedOutput(); err != nil {
		t.Fatalf("Wasm constant-fold assertions: %v\n%s", err, out)
	}
}
