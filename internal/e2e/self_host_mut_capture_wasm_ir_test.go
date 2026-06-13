package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMutCaptureWasmIR is the wasm sibling of TestSelfHostMutCaptureIR:
// mutable scalar captures (#2850) on the wasm stack-IR backend. The boxing
// pre-pass is backend-agnostic (lift_lambdas), so this checks wasm's codegen for
// the resulting i32[] array-cell capture + in-place .with. Expected values are
// the Go reference interpreter's (hardcoded — the native compiled backend shares
// the by-value bug). Exit codes are kept <= 125 (WASI proc_exit).
func TestSelfHostMutCaptureWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host mutable-capture wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"write-only", `function main(): i32 { var x = 1; var f = function (): i32 { x = 42; return 7; }; var r = f(); return r + x; }`, 49},
		{"counter", `function main(): i32 { var x = 0; var inc = function (): i32 { x = x + 1; return x; }; var a = inc(); var b = inc(); return x; }`, 2},
		{"counter-param", `function main(): i32 { var n = 10; var add = function (d: i32): i32 { n = n + d; return n; }; var a = add(5); var b = add(3); return n; }`, 18},
		{"read-only", `function main(): i32 { var x = 5; var f = function (): i32 { return x + 1; }; return f(); }`, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, "ir_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("mutable-capture wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
