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

func runBufferedWriterBytesCommand(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("writer bytes: %v\n%s", err, out)
	}
	checkBufferedWriterBytesOutput(t, out)
}

func checkBufferedWriterBytesOutput(t *testing.T, out []byte) {
	t.Helper()
	want := e2eharness.BufferedWriterBytesOutput()
	// Some bootstrap runners print main's return value after its output.
	if !bytes.Equal(out, want) && !bytes.Equal(out, append(want, []byte("0\n")...)) {
		t.Fatalf("binary output differs: got %d bytes", len(out))
	}
}

func bufferedWriterBytesSource(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "writer.fern")
	if err := os.WriteFile(p, []byte(e2eharness.BufferedWriterBytesProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBufferedWriterBytesInterp(t *testing.T) {
	runBufferedWriterBytesCommand(t, exec.Command(buildLangBinForInterp(t), "-interp", bufferedWriterBytesSource(t)))
}

const bufferedWriterFreshBytesProgram = `import "std/io_buffered" as io;
function main(): i32 {
  let seed = buf_new(3);
  for capacity in [1, 64] {
    let b = io.buf_writer_new(stdout(), capacity);
    for iteration in 0..32 {
      buf_push_byte(seed, 255); buf_push_byte(seed, 0); buf_push_byte(seed, 128);
      b = b.write_bytes(buf_take_bytes(seed));
      buf_push_byte(seed, 255); buf_push_byte(seed, 0); buf_push_byte(seed, 128);
      b = b.write_bytes_range(buf_take_bytes(seed), 1, 2);
      b = b.write_bytes(buf_take_bytes(seed));
      b = b.flush();
    }
    buf_free(b.handle());
  }
  buf_free(seed);
  return 0;
}`

func checkBufferedWriterBytesCensus(t *testing.T, out, diagnostic string, code int) {
	t.Helper()
	want := bytes.Repeat([]byte{255, 0, 128, 0}, 64)
	if !bytes.Equal([]byte(out), want) && !bytes.Equal([]byte(out), append(want, []byte("0\n")...)) {
		t.Fatalf("binary output differs: got %d bytes", len(out))
	}
	if code != 0 {
		t.Fatalf("writer bytes: exit %d\n%s", code, diagnostic)
	}
	allocs, frees, _ := parseLeakCheckLine(t, diagnostic)
	// stdout() returns a sentinel-headered handle on the bootstrap backends.
	// All arrays and writer buffers must be reclaimed, including fresh args.
	if allocs-frees > 2 {
		t.Fatalf("allocs=%d frees=%d: more than the two stdout handles survive\n%s", allocs, frees, diagnostic)
	}
}

func TestWasmBufferedWriterBytesCensus(t *testing.T) {
	out, diagnostic, code := runLeakCheckWasm(t, bufferedWriterFreshBytesProgram, false)
	checkBufferedWriterBytesCensus(t, out, diagnostic, code)
}

func TestArm64BufferedWriterBytesCensus(t *testing.T) {
	out, diagnostic, code := runLeakCheckArm64(t, bufferedWriterFreshBytesProgram)
	checkBufferedWriterBytesCensus(t, out, diagnostic, code)
}

func TestX86_64BufferedWriterBytesCensus(t *testing.T) {
	out, diagnostic, code := runLeakCheckX86_64(t, bufferedWriterFreshBytesProgram)
	checkBufferedWriterBytesCensus(t, out, diagnostic, code)
}

func TestArm64DarwinBufferedWriterBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	src := bufferedWriterBytesSource(t)
	bin := filepath.Join(t.TempDir(), "reader")
	cmd := exec.Command(buildLangBinForInterp(t), "-target", "arm64-darwin", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	runBufferedWriterBytesCommand(t, exec.Command(bin))
}

func TestArm64BufferedWriterBytes(t *testing.T) {
	out, code := runFixtureArm64(t, bufferedWriterBytesSource(t), "")
	checkBufferedWriterBytesOutput(t, []byte(out))
	if code != 0 {
		t.Fatalf("writer bytes: exit %d\n%s", code, out)
	}
}

func TestX86_64BufferedWriterBytes(t *testing.T) {
	out, code := runFixtureX86_64(t, bufferedWriterBytesSource(t), "")
	checkBufferedWriterBytesOutput(t, []byte(out))
	if code != 0 {
		t.Fatalf("writer bytes: exit %d\n%s", code, out)
	}
}

func TestWasmBufferedWriterBytes(t *testing.T) {
	program := e2eharness.BufferedWriterBytesProgram
	out, stderr, code := runWasmStdinEnv(t, program, "", nil)
	checkBufferedWriterBytesOutput(t, []byte(out))
	if code != 0 {
		t.Fatalf("writer bytes: exit %d\nstdout: %s\nstderr: %s", code, out, stderr)
	}
}

func TestWasmBufferedWriterBytesPreview1(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	src := bufferedWriterBytesSource(t)
	bin := filepath.Join(t.TempDir(), "reader.wasm")
	cmd := exec.Command(buildLangBinForInterp(t), "-target", "wasm32-wasi", "-emit", "command-module", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	runBufferedWriterBytesCommand(t, exec.Command("wasmtime", "run", bin))
}

func TestArm64SSABufferedWriterBytes(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	bin := compileArm64SSA(t, fern, e2eharness.BufferedWriterBytesProgram, os.Environ())
	runBufferedWriterBytesCommand(t, runArm64Bin(qemu, bin))
}

func TestX86_64SSABufferedWriterBytes(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	fern := buildFernCLI(t)
	bin := compileX86_64SSA(t, fern, e2eharness.BufferedWriterBytesProgram, os.Environ())
	cmd := exec.Command(bin)
	if qemu != "" {
		cmd = exec.Command(qemu, bin)
	}
	runBufferedWriterBytesCommand(t, cmd)
}
