package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"os/exec"
	"testing"
)

func TestSelfHostLexerX86_64(t *testing.T) {
	runner := x86_64Runner(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "lexer.fern")
	binPath := buildSelfHostBinFor(t, dir, "lexer.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port lexer assertion %d failed", code)
	}
}

func TestSelfHostLexerArm64(t *testing.T) {
	qemu := arm64Runner(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "lexer.fern")
	binPath := buildSelfHostBinFor(t, dir, "lexer.fern", "prog", e2eharness.TargetArm64Linux)
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port lexer assertion %d failed", code)
	}
}

func TestSelfHostLexerWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "lexer.fern")
	bin := buildSelfHostBinFor(t, dir, "lexer.fern", "lexer.wasm", "wasm32-wasi")
	if out, err := exec.Command(wasmtime, "run", bin).CombinedOutput(); err != nil {
		t.Fatalf("lexer assertions: %v\n%s", err, out)
	}
}
