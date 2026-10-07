package main

import (
	"os/exec"
	"strings"
	"testing"
)

// -run on a wasm32-wasi target runs the output under wasmtime. It used to go
// through the qemu path like any non-host ISA, which handed qemu-aarch64 a
// wasm binary that it rejected with exit 1 and no output.
func TestRunWasmUnderWasmtime(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	bin := buildFernForStdoutTest(t)
	entry := writeFern(t, "function main(): i32 {\n  let a: string[] = args();\n  print(a[1]);\n  return 3;\n}\n")
	for _, tc := range []struct {
		name string
		emit []string
		code int
	}{
		// A wasi:cli/run component reports ok or err, so 3 arrives as 1.
		{"component", nil, 1},
		{"command-module", []string{"-emit", "command-module"}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append(append([]string{"-target", "wasm32-wasi"}, tc.emit...), "-run", entry, "--", "hello")
			cmd := exec.Command(bin, args...)
			stdout, _ := cmd.Output()
			if got := strings.TrimSpace(string(stdout)); got != "hello" {
				t.Errorf("stdout %q, want %q", got, "hello")
			}
			if got := cmd.ProcessState.ExitCode(); got != tc.code {
				t.Errorf("exit %d, want %d", got, tc.code)
			}
		})
	}
}

func TestWasmRunRefusal(t *testing.T) {
	if err := wasmRunRefusal(compileRequest{target: "wasm32-wasi-http"}); err == nil || !strings.Contains(err.Error(), "wasmtime serve") {
		t.Errorf("wasm32-wasi-http: %v, want a refusal naming wasmtime serve", err)
	}
	if err := wasmRunRefusal(compileRequest{target: "wasm32-wasi"}); err != nil {
		t.Errorf("wasm32-wasi: %v, want no refusal", err)
	}
}
