package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostIOAllBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi", "wasm32-preview2"} {
		for _, tc := range []struct {
			name, source string
			input        []byte
		}{
			{"borrowed", e2eharness.IOAllBytesProgram, e2eharness.IOAllBytesInput()},
			{"stdin", e2eharness.IOStdinBytesProgram("io.read_all_stdin_bytes()", false), e2eharness.IOAllBytesInput()},
			{"dash", e2eharness.IOStdinBytesProgram(`io.read_input_bytes("-")`, false), e2eharness.IOAllBytesInput()},
			{"empty", e2eharness.IOStdinBytesProgram(`io.read_input_bytes("")`, true), nil},
		} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if target == "wasm32-preview2" {
					if _, err := exec.LookPath("wasmtime"); err != nil {
						t.Skip("requires wasmtime")
					}
					dir := t.TempDir()
					src, bin := filepath.Join(dir, "reader.fern"), filepath.Join(dir, "reader.wasm")
					if err := os.WriteFile(src, []byte(tc.source), 0o644); err != nil {
						t.Fatal(err)
					}
					compile := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-o", bin, src, cli.stdlib)
					if out, err := compile.CombinedOutput(); err != nil {
						t.Fatalf("component build: %v\n%s", err, out)
					}
					run := exec.Command("wasmtime", "run", bin)
					run.Stdin = bytes.NewReader(tc.input)
					if out, err := run.CombinedOutput(); err != nil {
						t.Fatalf("component run: %v\n%s", err, out)
					}
					return
				}
				stderr, code := cli.exitOfStdin(t, tc.source, target, tc.input, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("exit = %d, want 0\n%s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}
