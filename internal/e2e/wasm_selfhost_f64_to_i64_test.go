package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestWasmSelfHostF64ToI64 guards the f64→i64 cast (`x as i64` where x is f64)
// on the self-host wasm IR path. TestWasm*-named so test-e2e-wasm runs it under
// wasmtime in CI (TestSelfHostWasmRun is skipped there for lack of wasmtime).
//
// The bug it guards: `as_i64` always emitted op_int_extend (i32→i64), so an
// f64 operand had its bit pattern mis-extended as if it were an i32 — and on
// wasm the i32→i64 path fed an f64 to i64.extend_i32_s, an invalid module
// (wasmtime exits 1). Fixed by a new op_f64_to_i64 (i64.trunc_f64_s on wasm;
// the same 64-bit-dest cvttsd2si / fcvtzs the register backends already use
// for f64_to_i32), selected when the cast operand is f64.
func TestWasmSelfHostF64ToI64(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm f64-to-i64 e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name   string
		source string
		stdout string
	}{
		// Via a typed local, and directly on a literal — both > 2^32 so an i32
		// truncation would print the wrong value.
		{"via-local", "function main(): i32 { var x: f64 = 5000000000.0; var r: i64 = x as i64; print_int(r); return 0; }", "5000000000"},
		{"direct-print", "function main(): i32 { print_int(9000000000.0 as i64); return 0; }", "9000000000"},
		// Truncation toward zero (3.9 → 3) and a negative value.
		{"truncates", "function main(): i32 { var x: f64 = 3.9; print_int(x as i64); return 0; }", "3"},
		{"negative", "function main(): i32 { var x: f64 = -4000000000.0; print_int(x as i64); return 0; }", "-4000000000"},
		// Result of an f64 expression cast to i64.
		{"expr", "function main(): i32 { var a: f64 = 2500000000.0; print_int((a * 2.0) as i64); return 0; }", "5000000000"},
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
