package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
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
	stdlib, err := filepath.Abs("../../stdlib")
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
	cmd := runX86_64Bin(c.runner, c.bin, "-target", target, "-emit", "asm", "-o", out, src, c.stdlib)
	cmd.Env = append(os.Environ(), env...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI -target %s %s: %v\n%s", target, src, err, msg)
	}
	return out
}

// wasmComponent compiles src to a wasm32-wasi component, the target's
// default output form.
func (c *selfHostCLI) wasmComponent(t *testing.T, src string, env ...string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "main.wasm")
	cmd := runX86_64Bin(c.runner, c.bin, "-target", "wasm32-wasi", "-o", bin, src, c.stdlib)
	cmd.Env = append(os.Environ(), env...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI -target wasm32-wasi %s: %v\n%s", src, err, msg)
	}
	return bin
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

// arm64Binary compiles src for arm64 Linux the way `fern -target arm64-linux
// -o` does: through the self-host's own assembler and ELF writer, not GNU as.
func (c *selfHostCLI) arm64Binary(t *testing.T, src string, env ...string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "prog")
	cmd := runX86_64Bin(c.runner, c.bin, "-target", "arm64-linux", "-o", bin, src, c.stdlib)
	cmd.Env = append(os.Environ(), env...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI -target arm64-linux %s: %v\n%s", src, err, msg)
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// runWasmCensus runs a WAT module under wasmtime with args, returning its
// stderr (where the leakcheck census lands) and exit code.
func runWasmCensus(t *testing.T, wat string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(e2eharness.Wasmtime(t), append([]string{"run", wat}, args...)...)
	var eb bytes.Buffer
	cmd.Stderr = &eb
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally for %s\n%s", wat, eb.String())
	}
	return eb.String(), cmd.ProcessState.ExitCode()
}

// exitOf compiles the program text source for target (x86-64-linux,
// arm64-linux or wasm32-wasi), runs it, and returns its stderr and exit code.
func (c *selfHostCLI) exitOf(t *testing.T, source, target string, env ...string) (string, int) {
	t.Helper()
	return c.exitOfStdin(t, source, target, nil, env...)
}

func (c *selfHostCLI) exitOfStdin(t *testing.T, source, target string, stdin []byte, env ...string) (string, int) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return c.exitOfFile(t, src, target, stdin, env...)
}

// exitOfFile also supports fixtures with relative imports beside the entry.
func (c *selfHostCLI) exitOfFile(t *testing.T, src, target string, stdin []byte, env ...string) (string, int) {
	t.Helper()
	return c.exitOfFileArgs(t, src, target, stdin, nil, env...)
}

// exitOfFileArgs is exitOfFile with args passed to the program after its name.
func (c *selfHostCLI) exitOfFileArgs(t *testing.T, src, target string, stdin []byte, args []string, env ...string) (string, int) {
	t.Helper()
	var cmd *exec.Cmd
	switch target {
	case "x86-64-linux":
		cmd = runX86_64Bin(c.runner, c.x86Binary(t, src, env...), args...)
	case "arm64-linux":
		armgcc, qemu := arm64Tooling(t)
		asm, err := os.ReadFile(c.emit(t, src, target, env...))
		if err != nil {
			t.Fatal(err)
		}
		cmd = runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "prog", string(asm)), args...)
	case "wasm32-wasi":
		cmd = exec.Command(e2eharness.Wasmtime(t), append([]string{"run", c.emit(t, src, target, env...)}, args...)...)
	default:
		t.Fatalf("exitOf: unsupported target %s", target)
	}
	cmd.Stdin = bytes.NewReader(stdin)
	var eb bytes.Buffer
	cmd.Stderr = &eb
	_ = cmd.Run()
	if target == "wasm32-wasi" && (cmd.ProcessState == nil || !cmd.ProcessState.Exited()) {
		t.Fatalf("wasmtime did not exit normally\n%s", eb.String())
	}
	return eb.String(), cmd.ProcessState.ExitCode()
}
