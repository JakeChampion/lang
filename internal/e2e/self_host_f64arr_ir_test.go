package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostF64ArrayWasmIR is the CORRECTNESS gate for f64 arrays on the wasm
// IR backend (wasm_ir.fern). It is NOT a differential test against the wasm AST
// path: the wasm AST backend still stores array elements at a 4-byte stride
// (truncating f64), so AST and IR DISAGREE on f64 arrays — which is exactly the
// bug the IR path fixes. Per the project's IR-widening policy a legacy-AST gap
// that the IR path closes does not need fixing in the AST backend, so instead of
// AST==IR this test pins each program's IR result to a hardcoded oracle value
// (the native interpreter's / hand-computed answer).
//
// Coverage: f64 array literal + indexed read, indexed write (a[i] = v), a counted
// read loop, an f64[] param, and expression-valued elements — every f64-array
// shape the IR lowers (arr_make / arr_get / arr_set width 64 -> 8-byte stride +
// f64.load/store on wasm). f64[] returns, slices and for-in iteration stay on the
// AST path by design (the whole-module eligibility gate bails them).
func TestSelfHostF64ArrayWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host f64-array wasm IR e2e")
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

	// runIR pipes src to the driver with `-ir` (IR path for eligible modules),
	// runs the emitted WAT under wasmtime, returns the exit code.
	runIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		watFile := filepath.Join(dir, "ir_prog.wat")
		if err := os.WriteFile(watFile, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		run := exec.Command("wasmtime", "run", watFile)
		_ = run.Run()
		if run.ProcessState == nil || !run.ProcessState.Exited() {
			t.Fatalf("wasmtime did not exit normally for %q:\n%s", src, wat)
		}
		return run.ProcessState.ExitCode()
	}

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// literal + indexed read: 1.5 + 2.5 = 4.0 > 3.0 -> 7
		{"read", `function main(): i32 { var a: f64[] = [1.5, 2.5]; var x: f64 = a[0] + a[1]; if (x > 3.0) { return 7; } return 0; }`, 7},
		// indexed write: a[1] = 5.5; 1.0 + 5.5 = 6.5 > 6.0 -> 8
		{"write", `function main(): i32 { var a: f64[] = [1.0, 2.0]; a[1] = 5.5; var x: f64 = a[0] + a[1]; if (x > 6.0) { return 8; } return 0; }`, 8},
		// counted read loop: 1.5 + 2.5 + 3.0 = 7.0 > 6.0 -> 9
		{"loop", `function main(): i32 { var a: f64[] = [1.5, 2.5, 3.0]; var s: f64 = 0.0; var i = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } if (s > 6.0) { return 9; } return 0; }`, 9},
		// f64[] param: 2.5 + 4.0 = 6.5 > 6.0 -> 5
		{"param", `function sum(a: f64[]): f64 { return a[0] + a[1]; } function main(): i32 { var arr: f64[] = [2.5, 4.0]; var r: f64 = sum(arr); if (r > 6.0) { return 5; } return 0; }`, 5},
		// expression-valued elements: a = [2.0, 4.0, 3.0]; a[1] + a[2] = 7.0 > 6.0 -> 6
		{"expr-elems", `function main(): i32 { var k: f64 = 2.0; var a: f64[] = [k, k * 2.0, k + 1.0]; var x: f64 = a[1] + a[2]; if (x > 6.0) { return 6; } return 0; }`, 6},
		// mixed-precision: read an f64 element, cast to i32. a[2] = 9.5 -> 9
		{"read-cast", `function main(): i32 { var a: f64[] = [7.5, 8.5, 9.5]; return a[2] as i32; }`, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("f64-array wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
