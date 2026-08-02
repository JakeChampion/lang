package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestWasmSelfHostF64Coerce guards int→f64 coercion in f64 arithmetic on the
// self-host wasm IR path. TestWasm*-named so test-e2e-wasm runs it under
// wasmtime in CI (TestSelfHostWasmRun is skipped there for lack of wasmtime).
//
// The bug it guards: an i32 operand mixed with an f64 (e.g. `3.5 + 2`) was
// lowered without widening — op_fbin then saw an i32 on the stack where f64
// was expected, so the module failed wasm validation ("expected f64, found
// i32") and wasmtime exited 1. Fixed by emitting op_i32_to_f64 on a 32-bit
// non-f64 operand of an f64 binary op.
func TestWasmSelfHostF64Coerce(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm f64-coerce e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name   string
		source string
		stdout string
	}{
		// int literal on the right / left of an f64 op, then truncate to print.
		{"add-int-rhs", "function main(): i32 { print_int((3.5 + 2) as i32); return 0; }", "5"},
		{"add-int-lhs", "function main(): i32 { print_int((2 + 3.5) as i32); return 0; }", "5"},
		{"sub-int", "function main(): i32 { print_int((10 - 2.5) as i32); return 0; }", "7"},
		{"mul-int", "function main(): i32 { print_int((1.5 * 4) as i32); return 0; }", "6"},
		// int var (not just literal) mixed with f64.
		{"add-int-var", "function main(): i32 { var n: i32 = 6; print_int((n + 0.5) as i32); return 0; }", "6"},
		// f64 compare against an int operand (compare also goes through op_fbin).
		{"cmp-int", "function main(): i32 { if (2.5 > 2) { print_int(1); } else { print_int(0); } return 0; }", "1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.source))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(t.TempDir(), "prog.wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			out, _ := cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s: wasm exited %d, want 0\n--- WAT ---\n%s", tc.name, code, wat)
			}
			if string(out) != tc.stdout {
				t.Errorf("%s: wasm stdout = %q, want %q", tc.name, string(out), tc.stdout)
			}
		})
	}
}
