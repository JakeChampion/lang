package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArrMethods64bitIR is the correctness gate for METHODS that return
// f64[] / i64[] on the wasm IR backend. The array registries record them keyed
// "<Type>.<method>" (f64arr_ret_fns_of / i64arr_ret_fns_of) so the call site
// element-width-tracks the result (expr_is_f64arr / expr_is_i64arr / the
// arr_index_is_* method-call cases) and a later x[i] reads an 8-byte f64/i64.
// The body is just the union of the already-working method + array-return
// machineries. Results pinned to hardcoded oracle values.
func TestSelfHostArrMethods64bitIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arr-methods-64bit wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

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
		// f64[]-returning method, result bound then indexed. v[1] = 2.5 > 2.0 -> 6
		{"f64arr-method-binding", `struct Box { n: i32 } function (b: Box) vals(): f64[] { var a: f64[] = [1.5, 2.5, 3.5]; return a; } function main(): i32 { var b: Box = Box { n: 0 }; var v: f64[] = b.vals(); if (v[1] > 2.0) { return 6; } return 1; }`, 6},
		// i64[]-returning method, result bound then indexed. v[1] = 2e10 > 1.5e10 -> 7
		{"i64arr-method-binding", `struct Box { n: i32 } function (b: Box) vals(): i64[] { var a: i64[] = [10000000000, 20000000000]; return a; } function main(): i32 { var b: Box = Box { n: 0 }; var v: i64[] = b.vals(); if (v[1] > 15000000000) { return 7; } return 1; }`, 7},
		// Direct index of the i64[]-returning method call: obj.m()[i]. 9e9 > 8e9 -> 9
		{"i64arr-method-direct-index", `struct Box { n: i32 } function (b: Box) vals(): i64[] { var a: i64[] = [6000000000, 9000000000]; return a; } function main(): i32 { var b: Box = Box { n: 0 }; if (b.vals()[1] > 8000000000) { return 9; } return 1; }`, 9},
		// f64[] method elements used in arithmetic. 2.5 + 4.0 = 6.5 > 6.0 -> 5
		{"f64arr-method-arith", `struct Box { n: i32 } function (b: Box) vals(): f64[] { var a: f64[] = [2.5, 4.0]; return a; } function main(): i32 { var b: Box = Box { n: 0 }; var v: f64[] = b.vals(); var s: f64 = v[0] + v[1]; if (s > 6.0) { return 5; } return 1; }`, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("arr-methods-64bit wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
