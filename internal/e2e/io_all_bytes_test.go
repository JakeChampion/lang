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

func TestIOAllBytes(t *testing.T) {
	fern := buildLangBinForInterp(t)
	for _, tc := range []struct {
		name, source string
		input        []byte
	}{
		{"borrowed", e2eharness.IOAllBytesProgram, e2eharness.IOAllBytesInput()},
		{"stdin", e2eharness.IOStdinBytesProgram("io.read_all_stdin_bytes()", false), e2eharness.IOAllBytesInput()},
		{"dash", e2eharness.IOStdinBytesProgram(`io.read_input_bytes("-")`, false), e2eharness.IOAllBytesInput()},
		{"empty", e2eharness.IOStdinBytesProgram(`io.read_input_bytes("")`, true), nil},
	} {
		for _, target := range []string{"interp", "arm64-darwin", "x86-64-linux", "arm64-linux", "wasm32-wasi", "wasm32-preview2"} {
			t.Run(tc.name+"/"+target, func(t *testing.T) {
				var runner []string
				switch target {
				case "arm64-darwin":
					if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
						t.Skip("requires Apple Silicon")
					}
				case "x86-64-linux":
					_, runner = x86_64Tooling(t)
				case "arm64-linux":
					_, qemu := arm64Tooling(t)
					if qemu != "" {
						runner = []string{qemu}
					}
				case "wasm32-wasi", "wasm32-preview2":
					if _, err := exec.LookPath("wasmtime"); err != nil {
						t.Skip("requires wasmtime")
					}
					runner = []string{"wasmtime", "run"}
				}
				src := filepath.Join(t.TempDir(), "main.fern")
				if err := os.WriteFile(src, []byte(tc.source), 0o644); err != nil {
					t.Fatal(err)
				}
				var cmd *exec.Cmd
				if target == "interp" {
					cmd = exec.Command(fern, "-interp", src)
				} else {
					bin := filepath.Join(t.TempDir(), "reader")
					compileTarget := target
					if target == "wasm32-preview2" {
						compileTarget = "wasm32-wasi"
					}
					args := []string{"-target", compileTarget, "-o", bin, src}
					if target == "wasm32-wasi" {
						args = append([]string{"-emit", "command-module"}, args...)
					}
					if out, err := exec.Command(fern, args...).CombinedOutput(); err != nil {
						t.Fatalf("compile: %v\n%s", err, out)
					}
					argv := append(append([]string{}, runner...), bin)
					cmd = exec.Command(argv[0], argv[1:]...)
				}
				cmd.Stdin = bytes.NewReader(tc.input)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("run: %v\n%s", err, out)
				}
			})
		}
	}
}
