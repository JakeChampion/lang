package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestWasmSelfHostI64Print is a CI-runnable regression guard for printing i64
// values through the self-host wasm IR path. Like the arr_push / random guards
// it is named TestWasm* so the wasmtime-enabled test-e2e-wasm workflow runs it
// (the TestSelfHostWasmRun suite is skipped in CI for lack of wasmtime).
//
// The bug it guards: i64 arithmetic/compare already worked on the IR path, but
// print_int(<i64>) lowered to the i32 helper $__fern_print_int — passing an i64
// to its i32 param is a wasm type mismatch, so every program printing an i64
// produced an invalid module (wasmtime exits 1). Fixed by lowering an i64
// print_int argument through op_print_i64 → $__fern_print_int64 (and gating
// that helper + the fd_write import on the new op), mirroring the AST path.
func TestWasmSelfHostI64Print(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm i64-print e2e")
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

	// Each prints a > 2^32 i64 value whose decimal text would be wrong (or the
	// module invalid) if the i64 were truncated to i32 / printed via print_int.
	cases := []struct {
		name   string
		source string
		stdout string
	}{
		{"literal", "function main(): i32 { var x: i64 = 5000000000; print_int(x); return 0; }", "5000000000"},
		{"add", "function main(): i32 { var a: i64 = 3000000000; var b: i64 = 2000000000; print_int(a + b); return 0; }", "5000000000"},
		{"mul", "function main(): i32 { var a: i64 = 100000; var b: i64 = 100000; print_int(a * b); return 0; }", "10000000000"},
		{"negative", "function main(): i32 { var a: i64 = 0; var b: i64 = 5000000000; print_int(a - b); return 0; }", "-5000000000"},
		{"div", "function main(): i32 { var a: i64 = 10000000000; print_int(a / 7); return 0; }", "1428571428"},
		{"func-return", "function big(): i64 { return 9000000000; } function main(): i32 { print_int(big()); return 0; }", "9000000000"},
		{"param", "function dbl(x: i64): i64 { return x * 2; } function main(): i32 { print_int(dbl(3000000000)); return 0; }", "6000000000"},
		{"loop-accumulate", "function main(): i32 { var sum: i64 = 0; var i: i32 = 0; while (i < 5) { sum = sum + 1000000000; i = i + 1; } print_int(sum); return 0; }", "5000000000"},
		// A module that prints BOTH widths: print_int (i32) + print_int64 (i64)
		// must coexist (distinct helpers, non-overlapping scratch buffers).
		{"mixed-widths", "function main(): i32 { var n: i64 = 8000000000; print_int(42); print_int(n); return 0; }", "428000000000"},
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
