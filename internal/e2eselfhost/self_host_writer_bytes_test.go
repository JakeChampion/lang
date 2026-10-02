package e2eselfhost

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

func TestSelfHostWriterBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	// Keep stderr open for the ownership census. These executable entry points
	// exit with main's result instead of printing it to the closed stdout.
	source := strings.Replace(e2eharness.WriterBytesProgram, "var closed = stderr();", "var closed = stdout();", 1)
	src := filepath.Join(t.TempDir(), "writer.fern")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, mode := range []struct {
			name string
			env  []string
		}{
			{"checked", []string{"FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"}},
			{"plain", nil},
		} {
			t.Run(target+"/"+mode.name, func(t *testing.T) {
				var cmd *exec.Cmd
				switch target {
				case "x86-64-linux":
					cmd = runX86_64Bin(cli.runner, cli.x86Binary(t, src, mode.env...))
				case "arm64-linux":
					gcc, qemu := arm64Tooling(t)
					asm, err := os.ReadFile(cli.emit(t, src, target, mode.env...))
					if err != nil {
						t.Fatal(err)
					}
					cmd = runArm64Bin(qemu, buildBinArm64(t, gcc, t.TempDir(), "writer", string(asm)))
				case "wasm32-wasi":
					cmd = exec.Command("wasmtime", "run", cli.emit(t, src, target, mode.env...))
				}
				var out, diagnostic bytes.Buffer
				cmd.Stdout = &out
				cmd.Stderr = &diagnostic
				if err := cmd.Run(); err != nil {
					t.Fatalf("write: %v\n%s", err, diagnostic.String())
				}
				if !bytes.Equal(out.Bytes(), e2eharness.WriterBytesOutput()) {
					t.Fatalf("binary output differs: got %d bytes\n%s", out.Len(), diagnostic.String())
				}
				if mode.name == "checked" {
					if strings.Contains(diagnostic.String(), "fern-sanitizer:") {
						t.Fatal(diagnostic.String())
					}
					assertBalancedCensus(t, diagnostic.String())
				}
			})
		}
	}
}

func testWriterBytesComponents(t *testing.T, compiler string, runner []string, stdlib string) {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("requires wasmtime")
	}
	// Primary components already provide stdout and stderr writes, but not
	// descriptor closing. Exercise the raw write contract on those streams.
	source, _, ok := strings.Cut(e2eharness.WriterBytesProgram, " var closed = stderr();")
	if !ok {
		t.Fatal("missing boundary before closed-descriptor cases")
	}
	source += " return 0;\n}\n"
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run("component/"+stream, func(t *testing.T) {
			dir := t.TempDir()
			src, bin := filepath.Join(dir, "writer.fern"), filepath.Join(dir, "writer.wasm")
			text := strings.Replace(source, "var w = stdout();", "var w = "+stream+"();", 1)
			if err := os.WriteFile(src, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			compile := runX86_64Bin(runner, compiler, "-target", "wasm32-wasi", src, stdlib, "-o", bin)
			compile.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("component compile: %v\n%s", err, out)
			}
			run := exec.Command("wasmtime", "run", bin)
			var stdout, stderr bytes.Buffer
			run.Stdout, run.Stderr = &stdout, &stderr
			if err := run.Run(); err != nil {
				t.Fatalf("component run: %v\n%s", err, stderr.String())
			}
			data, empty := stdout.Bytes(), stderr.Bytes()
			if stream == "stderr" {
				data, empty = stderr.Bytes(), stdout.Bytes()
			}
			if !bytes.Equal(data, e2eharness.WriterBytesOutput()) || len(empty) != 0 {
				t.Fatalf("component binary output differs: data=%d other=%d", len(data), len(empty))
			}
		})
	}
}

func TestSelfHostWriterBytesComponents(t *testing.T) {
	cli := buildSelfHostCLI(t)
	testWriterBytesComponents(t, cli.bin, cli.runner, cli.stdlib)
}

func TestSelfHostArm64DarwinWriterBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "writer.fern")
	if err := os.WriteFile(src, []byte(strings.Replace(e2eharness.WriterBytesProgram, "var closed = stderr();", "var closed = stdout();", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, checked := range []bool{true, false} {
		t.Run(map[bool]string{true: "checked", false: "plain"}[checked], func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "writer")
			compile := exec.Command(cli, "-target", "arm64-darwin", src, stdlib, "-o", bin)
			compile.Env = os.Environ()
			if checked {
				compile.Env = append(compile.Env, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1")
			}
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			cmd := exec.Command(bin)
			var output, diagnostic bytes.Buffer
			cmd.Stdout = &output
			cmd.Stderr = &diagnostic
			err := cmd.Run()
			out := diagnostic.Bytes()
			if !bytes.Equal(output.Bytes(), e2eharness.WriterBytesOutput()) {
				t.Fatalf("binary output differs: %d bytes\n%s", output.Len(), out)
			}
			if err != nil || strings.Contains(string(out), "fern-sanitizer:") {
				t.Fatalf("writer bytes: %v\n%s", err, out)
			}
			if checked {
				assertBalancedCensus(t, string(out))
			}
		})
	}
	testWriterBytesComponents(t, cli, nil, stdlib)
}
