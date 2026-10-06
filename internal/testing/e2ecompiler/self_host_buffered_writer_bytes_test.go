package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostBufferedWriterBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := e2eharness.BufferedWriterBytesProgram
	src := filepath.Join(t.TempDir(), "writer.fern")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, mode := range []struct {
			name string
			env  []string
		}{
			{"checked", []string{"FERN_SANITIZE=1", "FERN_LEAKCHECK=1"}},
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
				if !bytes.Equal(out.Bytes(), e2eharness.BufferedWriterBytesOutput()) {
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

func TestSelfHostArm64DarwinBufferedWriterBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "writer.fern")
	if err := os.WriteFile(src, []byte(e2eharness.BufferedWriterBytesProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, checked := range []bool{true, false} {
		t.Run(map[bool]string{true: "checked", false: "plain"}[checked], func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "writer")
			compile := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, stdlib)
			compile.Env = os.Environ()
			if checked {
				compile.Env = append(compile.Env, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
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
			if !bytes.Equal(output.Bytes(), e2eharness.BufferedWriterBytesOutput()) {
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
}
