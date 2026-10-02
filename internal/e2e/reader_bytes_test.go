package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func runReaderBytesCommand(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	cmd.Stdin = bytes.NewReader(e2eharness.ReaderBytesInput())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reader bytes: %v\n%s", err, out)
	}
}

func readerBytesSource(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "reader.fern")
	if err := os.WriteFile(p, []byte(e2eharness.ReaderBytesProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReaderBytesInterp(t *testing.T) {
	runReaderBytesCommand(t, exec.Command(buildLangBinForInterp(t), "-interp", readerBytesSource(t)))
}

func TestArm64DarwinReaderBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	src := readerBytesSource(t)
	bin := filepath.Join(t.TempDir(), "reader")
	cmd := exec.Command(buildLangBinForInterp(t), "-target", "arm64-darwin", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	runReaderBytesCommand(t, exec.Command(bin))
}

func TestArm64ReaderBytes(t *testing.T) {
	out, code := runFixtureArm64(t, readerBytesSource(t), string(e2eharness.ReaderBytesInput()))
	if code != 0 {
		t.Fatalf("reader bytes: exit %d\n%s", code, out)
	}
}

func TestX86_64ReaderBytes(t *testing.T) {
	out, code := runFixtureX86_64(t, readerBytesSource(t), string(e2eharness.ReaderBytesInput()))
	if code != 0 {
		t.Fatalf("reader bytes: exit %d\n%s", code, out)
	}
}

func TestWasmReaderBytes(t *testing.T) {
	out, stderr, code := runWasmStdinEnv(t, e2eharness.ReaderBytesProgram, string(e2eharness.ReaderBytesInput()), nil)
	if code != 0 || out != "0\n" {
		t.Fatalf("reader bytes: exit %d\nstdout: %s\nstderr: %s", code, out, stderr)
	}
}

func TestWasmReaderBytesPreview1(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	src := readerBytesSource(t)
	bin := filepath.Join(t.TempDir(), "reader.wasm")
	cmd := exec.Command(buildLangBinForInterp(t), "-target", "wasm32-wasi", "-emit", "command-module", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	runReaderBytesCommand(t, exec.Command("wasmtime", "run", bin))
}

func TestArm64SSAReaderBytes(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	bin := compileArm64SSA(t, fern, e2eharness.ReaderBytesProgram, os.Environ())
	runReaderBytesCommand(t, runArm64Bin(qemu, bin))
}

func TestX86_64SSAReaderBytes(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	fern := buildFernCLI(t)
	bin := compileX86_64SSA(t, fern, e2eharness.ReaderBytesProgram, os.Environ())
	cmd := exec.Command(bin)
	if qemu != "" {
		cmd = exec.Command(qemu, bin)
	}
	runReaderBytesCommand(t, cmd)
}
