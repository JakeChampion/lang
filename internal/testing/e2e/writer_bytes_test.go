package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
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

func compileCLIWriterBytes(t *testing.T, target, source string, census bool) string {
	t.Helper()
	dir := t.TempDir()
	src, bin := filepath.Join(dir, "writer.fern"), filepath.Join(dir, "writer")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(buildLangBinForInterp(t), "-target", target, "-o", bin, src)
	cmd.Env = e2eharness.ChildEnv()
	if census {
		cmd.Env = append(cmd.Env, "FERN_LEAKCHECK=1")
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	return bin
}

func TestWriterBytesInterp(t *testing.T) {
	runWriterBytesCommand(t, exec.Command(buildLangBinForInterp(t), "-interp", writerBytesSource(t)))
}

func TestWriterBytesCensus(t *testing.T) {
	// Writes must retain nothing: compare a no-write control with repeated
	// writes. TestSelfHostWriterBytes separately requires a zero-live census
	// for the complete fixture, including short strings and closed-handle errors.
	for _, tc := range []struct {
		name string
		run  func(*testing.T, string) (string, string, int)
	}{
		{"x86_64", func(t *testing.T, source string) (string, string, int) {
			runner := e2eharness.X86_64Runner(t)
			bin := compileCLIWriterBytes(t, "x86-64-linux", source, true)
			return runSplit(t, runX86_64Bin(runner, bin))
		}},
		{"arm64", func(t *testing.T, source string) (string, string, int) {
			qemu := e2eharness.Arm64Runner(t)
			bin := compileCLIWriterBytes(t, "arm64-linux", source, true)
			return runSplit(t, runArm64Bin(qemu, bin))
		}},
		{"wasm", func(t *testing.T, source string) (string, string, int) {
			return runComponent(t, buildLeakCheckCLIComponent(t, source, false), runOpts{})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, c := range []struct{ name, argument, output string }{
				{"array", "a", "\x00\xff\x80"},
				{"array_literal", "[0 as u8, 255 as u8, 128 as u8]", "\x00\xff\x80"},
				{"array_call", "fresh_array()", "\x00\xff\x80"},
				{"array_subview", "a[1:3]", "\xff\x80"},
				{"view", "v", "0123456789abcdefghij"},
				{"string_view", "s.as_bytes()", "0123456789abcdefghij"},
				{"string_call", `fresh_text("0123456789", "abcdefghij").as_bytes()`, "0123456789abcdefghij"},
			} {
				t.Run(c.name, func(t *testing.T) {
					var controlLive, controlBlocks int64
					for _, writes := range []int{0, 24} {
						source := fmt.Sprintf(`
@noinline function fresh_array(): u8[] { return [0 as u8, 255 as u8, 128 as u8]; }
@noinline function fresh_text(a: string, b: string): string { return a + b; }
function main(): i32 {
 let w = stdout();
 let a: u8[] = fresh_array();
 let s = fresh_text("0123456789", "abcdefghij");
 let v: [u8] = s.as_bytes();
 let i = 0;
 while (i < %d) {
  match (w.write_bytes(%s)) { Some(_) => { return 1; }, None => {} }
  match (w.write_some_bytes(%s)) { Err(_) => { return 2; }, Ok(n) => { if (n != %d) { return 3; } } }
  i = i + 1;
 }
 if (a[1] != 255 as u8 || s != "0123456789abcdefghij" || v[19] != 106 as u8) { return 4; }
 return 0;
}`, writes, c.argument, c.argument, len(c.output))
						out, diagnostic, code := tc.run(t, source)
						if code != 0 || out != strings.Repeat(c.output, 2*writes) {
							t.Fatalf("%d writes: exit=%d output=%q\n%s", writes, code, out, diagnostic)
						}
						allocs, frees, live := parseLeakCheckLine(t, diagnostic)
						if writes == 0 {
							controlLive, controlBlocks = live, allocs-frees
						} else if live != controlLive || allocs-frees != controlBlocks {
							t.Fatalf("writes retain storage: control live=%d blocks=%d; %s", controlLive, controlBlocks, diagnostic)
						}
					}
				})
			}
		})
	}
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
	// The shared fixture runners use the primary compiler. Exercise the Go
	// CLI explicitly here; e2ecompiler owns the primary target matrix.
	qemu := e2eharness.Arm64Runner(t)
	bin := compileCLIWriterBytes(t, "arm64-linux", e2eharness.WriterBytesProgram, false)
	runWriterBytesCommand(t, runArm64Bin(qemu, bin))
}

func TestX86_64WriterBytes(t *testing.T) {
	runner := e2eharness.X86_64Runner(t)
	bin := compileCLIWriterBytes(t, "x86-64-linux", e2eharness.WriterBytesProgram, false)
	runWriterBytesCommand(t, runX86_64Bin(runner, bin))
}

func TestWasmWriterBytes(t *testing.T) {
	program := strings.Replace(e2eharness.WriterBytesProgram, "n <= 0 || n > 8192", "n != 4096", 1)
	out, stderr, code := runCLIComponent(t, program, runOpts{})
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
