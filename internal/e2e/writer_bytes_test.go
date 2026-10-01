package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func runWriterBytesCommand(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("writer bytes: %v\n%s", err, out)
	}
	checkWriterBytesOutput(t, out)
}

func checkWriterBytesOutput(t *testing.T, out []byte) {
	t.Helper()
	want := e2eharness.WriterBytesOutput()
	// Some bootstrap runners print main's return value after its output.
	if !bytes.Equal(out, want) && !bytes.Equal(out, append(want, []byte("0\n")...)) {
		t.Fatalf("binary output differs: got %d bytes", len(out))
	}
}

func writerBytesSource(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "writer.fern")
	if err := os.WriteFile(p, []byte(e2eharness.WriterBytesProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWriterBytesInterp(t *testing.T) {
	runWriterBytesCommand(t, exec.Command(buildLangBinForInterp(t), "-interp", writerBytesSource(t)))
}

func TestArm64DarwinWriterBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	src := writerBytesSource(t)
	bin := filepath.Join(t.TempDir(), "reader")
	cmd := exec.Command(buildLangBinForInterp(t), "-target", "arm64-darwin", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	runWriterBytesCommand(t, exec.Command(bin))
}

func TestArm64WriterBytes(t *testing.T) {
	out, code := runFixtureArm64(t, writerBytesSource(t), "")
	checkWriterBytesOutput(t, []byte(out))
	if code != 0 {
		t.Fatalf("writer bytes: exit %d\n%s", code, out)
	}
}

func TestX86_64WriterBytes(t *testing.T) {
	out, code := runFixtureX86_64(t, writerBytesSource(t), "")
	checkWriterBytesOutput(t, []byte(out))
	if code != 0 {
		t.Fatalf("writer bytes: exit %d\n%s", code, out)
	}
}

func TestWasmWriterBytes(t *testing.T) {
	program := strings.Replace(e2eharness.WriterBytesProgram, "n <= 0 || n > 8192", "n != 4096", 1)
	out, stderr, code := runWasmStdinEnv(t, program, "", nil)
	checkWriterBytesOutput(t, []byte(out))
	if code != 0 {
		t.Fatalf("writer bytes: exit %d\nstdout: %s\nstderr: %s", code, out, stderr)
	}
}

func TestWasmWriterBytesPreview1(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	src := writerBytesSource(t)
	bin := filepath.Join(t.TempDir(), "reader.wasm")
	cmd := exec.Command(buildLangBinForInterp(t), "-target", "wasm32-wasi", "-emit", "command-module", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	runWriterBytesCommand(t, exec.Command("wasmtime", "run", bin))
}

func TestArm64SSAWriterBytes(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	bin := compileArm64SSA(t, fern, e2eharness.WriterBytesProgram, os.Environ())
	runWriterBytesCommand(t, runArm64Bin(qemu, bin))
}

func TestX86_64SSAWriterBytes(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	fern := buildFernCLI(t)
	bin := compileX86_64SSA(t, fern, e2eharness.WriterBytesProgram, os.Environ())
	cmd := exec.Command(bin)
	if qemu != "" {
		cmd = exec.Command(qemu, bin)
	}
	runWriterBytesCommand(t, cmd)
}
