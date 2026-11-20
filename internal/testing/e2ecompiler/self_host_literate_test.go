package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"testing"
)

func TestSelfHostLiterateX86_64(t *testing.T) {
	runner := x86_64Runner(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/literate_check_run.fern")
	binPath := buildSelfHostBinFor(t, dir, "drivers/literate_check_run.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("literate self-checks: %v\n%s", err, out)
	}
}

func TestSelfHostLiterateArm64(t *testing.T) {
	qemu := arm64Runner(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/literate_check_run.fern")
	binPath := buildSelfHostBinFor(t, dir, "drivers/literate_check_run.fern", "prog", e2eharness.TargetArm64Linux)
	cmd := runArm64Bin(qemu, binPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("literate self-checks: %v\n%s", err, out)
	}
}

func TestSelfHostLiterateWASM(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/literate_check_run.fern")
	core := buildSelfHostBinFor(t, dir, "drivers/literate_check_run.fern", "prog.wasm", e2eharness.TargetWasm32Wasi)
	cmd := e2eharness.RunWasmCore(t, core)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("literate self-checks: %v\n%s", err, out)
	}
}
