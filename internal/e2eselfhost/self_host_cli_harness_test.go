package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// selfHostCLI is the self-host front end (fern.fern) built once per test, for
// legs that must see what production sees: the checker's annotations, module
// loading and flattening, none of which the emit drivers run.
type selfHostCLI struct {
	gcc, stdlib string
	runner      []string
	bin         string
}

func buildSelfHostCLI(t *testing.T) *selfHostCLI {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	bin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	return &selfHostCLI{gcc: gcc, stdlib: stdlib, runner: runner, bin: bin}
}

// emit compiles the entry file at src for target with env added to the
// compiler's environment, and returns the path of the emitted asm or WAT.
func (c *selfHostCLI) emit(t *testing.T, src, target string, env ...string) string {
	t.Helper()
	ext := ".s"
	if target == "wasm32-wasi" {
		ext = ".wat"
	}
	out := filepath.Join(t.TempDir(), "out"+ext)
	cmd := runX86_64Bin(c.runner, c.bin, "-target", target, "-emit", "asm", src, c.stdlib, "-o", out)
	cmd.Env = append(os.Environ(), env...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI -target %s %s: %v\n%s", target, src, err, msg)
	}
	return out
}

// x86Binary compiles src for x86-64 Linux and links it.
func (c *selfHostCLI) x86Binary(t *testing.T, src string, env ...string) string {
	t.Helper()
	asm := c.emit(t, src, "x86-64-linux", env...)
	bin := filepath.Join(filepath.Dir(asm), "prog")
	if msg, err := exec.Command(c.gcc, "-nostdlib", "-static", "-o", bin, asm).CombinedOutput(); err != nil {
		t.Fatalf("link %s: %v\n%s", src, err, msg)
	}
	return bin
}

// runWasmCensus runs a WAT module under wasmtime with args, returning its
// stderr (where the leakcheck census lands) and exit code.
func runWasmCensus(t *testing.T, wat string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("wasmtime", append([]string{"run", wat}, args...)...)
	var eb bytes.Buffer
	cmd.Stderr = &eb
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally for %s\n%s", wat, eb.String())
	}
	return eb.String(), cmd.ProcessState.ExitCode()
}

// exitOf compiles the program text source for target (x86-64-linux or
// wasm32-wasi), runs it, and returns its stderr and exit code.
func (c *selfHostCLI) exitOf(t *testing.T, source, target string, env ...string) (string, int) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	switch target {
	case "x86-64-linux":
		return runWithStdin(t, c.runner, c.x86Binary(t, src, env...), nil)
	case "wasm32-wasi":
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Fatal("wasmtime not on PATH")
		}
		return runWasmCensus(t, c.emit(t, src, target, env...))
	}
	t.Fatalf("exitOf: unsupported target %s", target)
	return "", 0
}
