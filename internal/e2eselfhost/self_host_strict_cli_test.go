package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// strictCLI is the self-hosted CLI (`fern.fern`) built for the x86-64 host,
// run with no FERN_ variable of the caller's, so a declaration the typed
// lowering refuses fails the compile.
type strictCLI struct {
	bin, stdlib, gcc string
	runner           []string
}

func newStrictCLI(t *testing.T) *strictCLI {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	stdlib, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	return &strictCLI{bin: buildSelfHostBin(t, gcc, dir, "fern.fern", "fern"), stdlib: stdlib, gcc: gcc, runner: runner}
}

// tryEmit compiles src for target and returns the assembly, or the CLI's
// diagnostics and error when it refuses. env is the compile's FERN_ variables.
func (c *strictCLI) tryEmit(t *testing.T, target, src string, env ...string) (string, string, error) {
	t.Helper()
	proj := t.TempDir()
	mainPath := filepath.Join(proj, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	asmPath := filepath.Join(proj, "main.s")
	cmd := runX86_64Bin(c.runner, c.bin, "-target", target, "-emit", "asm", "-o", asmPath, mainPath, c.stdlib)
	cmd.Env = childEnv(env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", stderr.String(), err
	}
	asm, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(asm), stderr.String(), nil
}

// emit is tryEmit that fails the test when the CLI refuses.
func (c *strictCLI) emit(t *testing.T, target, src string, env ...string) string {
	t.Helper()
	asm, diags, err := c.tryEmit(t, target, src, env...)
	if err != nil {
		t.Fatalf("compile for %s: %v\n%s\n--- source ---\n%s", target, err, diags, src)
	}
	return asm
}

// runX86 links x86-64 assembly and runs it, returning its exit code and stdout.
func (c *strictCLI) runX86(t *testing.T, asm string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(c.gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	run := runX86_64Bin(c.runner, binPath)
	var stdout bytes.Buffer
	run.Stdout = &stdout
	_ = run.Run()
	if run.ProcessState == nil {
		t.Fatalf("did not run: %s", binPath)
	}
	return run.ProcessState.ExitCode(), stdout.String()
}

// runArm64 assembles arm64 assembly with the aarch64 toolchain and runs it
// under qemu (or natively), returning its exit code and stdout.
func runArm64(t *testing.T, gcc, qemu, asm string) (int, string) {
	t.Helper()
	bin := buildBinArm64(t, gcc, t.TempDir(), "prog", asm)
	run := runArm64Bin(qemu, bin)
	var stdout bytes.Buffer
	run.Stdout = &stdout
	_ = run.Run()
	if run.ProcessState == nil {
		t.Fatalf("did not run: %s", bin)
	}
	return run.ProcessState.ExitCode(), stdout.String()
}

// runWasm runs WAT under wasmtime, returning its exit code and stdout. It
// skips the test when wasmtime is not on PATH.
func runWasm(t *testing.T, wat string) (int, string) {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	path := filepath.Join(t.TempDir(), "prog.wat")
	if err := os.WriteFile(path, []byte(wat), 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("wasmtime", path)
	var stdout bytes.Buffer
	run.Stdout = &stdout
	_ = run.Run()
	if run.ProcessState == nil {
		t.Fatalf("did not run: %s", path)
	}
	return run.ProcessState.ExitCode(), stdout.String()
}
