package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

type selfHostCLI struct {
	gcc, stdlib string
	runner      []string
	bin         string
	flags       []string
}

func (c *selfHostCLI) compileCmd(target, out, src string, extra ...string) *exec.Cmd {
	args := append([]string{"-target", target}, extra...)
	args = append(args, c.flags...)
	return runX86_64Bin(c.runner, c.bin, append(args, "-o", out, src, c.stdlib)...)
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

func buildRuntimeCLI(t *testing.T) *selfHostCLI {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	return &selfHostCLI{gcc: gcc, runner: runner, stdlib: e2eharness.SelfHostStdlibRoot(t),
		bin: e2eharness.CachedDriverBinFor(t, e2eharness.RepoPath("compiler"), "drivers/runtime_run.fern", e2eharness.TargetX86_64Linux)}
}

func (c *selfHostCLI) emit(t *testing.T, src, target string, env ...string) string {
	t.Helper()
	ext := ".s"
	if target == "wasm32-wasi" {
		ext = ".wat"
	}
	out := filepath.Join(t.TempDir(), "out"+ext)
	cmd := c.compileCmd(target, out, src, "-emit", "asm")
	cmd.Env = append(os.Environ(), env...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI -target %s %s: %v\n%s", target, src, err, msg)
	}
	return out
}

func (c *selfHostCLI) wasmComponent(t *testing.T, src string, env ...string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "main.wasm")
	cmd := c.compileCmd("wasm32-wasi", bin, src)
	cmd.Env = append(os.Environ(), env...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI -target wasm32-wasi %s: %v\n%s", src, err, msg)
	}
	return bin
}

func (c *selfHostCLI) x86Binary(t *testing.T, src string, env ...string) string {
	t.Helper()
	asm := c.emit(t, src, "x86-64-linux", env...)
	bin := filepath.Join(filepath.Dir(asm), "prog")
	if msg, err := exec.Command(c.gcc, "-nostdlib", "-static", "-o", bin, asm).CombinedOutput(); err != nil {
		t.Fatalf("link %s: %v\n%s", src, err, msg)
	}
	return bin
}

func (c *selfHostCLI) arm64Binary(t *testing.T, src string, env ...string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "prog")
	cmd := c.compileCmd("arm64-linux", bin, src)
	cmd.Env = append(os.Environ(), env...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI -target arm64-linux %s: %v\n%s", src, err, msg)
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

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

func (c *selfHostCLI) exitOfFile(t *testing.T, src, target string, stdin []byte, env ...string) (string, int) {
	t.Helper()
	return c.exitOfFileArgs(t, src, target, stdin, nil, env...)
}

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
