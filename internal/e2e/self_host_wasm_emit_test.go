package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostWasmRun exercises the self-hosted wasm emitter
// (examples/self_host/wasm.fern) end to end. wasm_run.fern reads Fern
// source from stdin, runs it through the self-host lexer + parser +
// wasm.emit_module, and prints a WASI core module in text format (WAT).
// For each case the test:
//
//  1. builds wasm_run.fern once with the Go x86-64 backend,
//  2. pipes the source in, capturing the emitted WAT,
//  3. runs it with `wasmtime run prog.wat`,
//  4. asserts the process exit code matches the program's result
//     (the emitter lowers `return <expr>;` to `proc_exit(<expr>)`).
//
// This is the wasm analogue of TestSelfHostAsmRunX86_64, and the first
// slice of the wasm backend on the path to retiring the Go wasm backend.
// Covered now: integer literals + unary `-` + binary `+ - * / %`.
func TestSelfHostWasmRun(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern", "wasm_run.fern"} {
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
		exit   int
	}{
		{"return-literal", "function main(): i32 { return 42; }", 42},
		{"bare-return", "return 42;", 42},
		{"arithmetic", "function main(): i32 { return 1 + 2 * 3; }", 7},
		{"parens", "function main(): i32 { return (1 + 2) * 3; }", 9},
		{"subtraction", "function main(): i32 { return 100 - 23; }", 77},
		{"division", "function main(): i32 { return 84 / 2; }", 42},
		{"modulo", "function main(): i32 { return 23 % 5; }", 3},
		{"unary-neg", "function main(): i32 { return 0 - 5 + 10; }", 5},
		{"nested", "function main(): i32 { return (2 + 3) * (4 + 4) - 1; }", 39},
		{"no-return-exits-0", "function main(): i32 { }", 0},
		// Locals + reassignment.
		{"locals", "function main(): i32 { var x = 5; var y = 10; return x + y; }", 15},
		{"reassign", "function main(): i32 { var x = 5; x = x + 3; return x; }", 8},
		{"compound-assign", "function main(): i32 { var x = 1; x *= 6; x += 1; return x; }", 7},
		// Comparisons (0/1 results).
		{"comparison-true", "function main(): i32 { return 5 < 10; }", 1},
		{"comparison-false", "function main(): i32 { return 10 < 5; }", 0},
		{"equality-true", "function main(): i32 { return 7 == 7; }", 1},
		// Logical + not.
		{"and", "function main(): i32 { if (true && true) { return 1; } return 0; }", 1},
		{"or", "function main(): i32 { if (false || true) { return 1; } return 0; }", 1},
		{"not", "function main(): i32 { if (!false) { return 1; } return 0; }", 1},
		{"and-with-comparison", "function main(): i32 { var x = 5; if (x > 0 && x < 10) { return 1; } return 0; }", 1},
		// if / else.
		{"if-then-branch", "function main(): i32 { var x = 5; if (x < 10) { return 1; } return 2; }", 1},
		{"if-else-branch", "function main(): i32 { var x = 20; if (x < 10) { return 1; } return 2; }", 2},
		{"if-else-explicit", "function main(): i32 { if (true) { return 9; } else { return 0; } }", 9},
		// while.
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s += i; i += 1; } return s; }", 15},
		{"while-early-return", "function main(): i32 { var i = 0; while (i < 100) { if (i == 7) { return i; } i += 1; } return 99; }", 7},
		// break / continue.
		{"break-in-while", "function main(): i32 { var i = 0; var s = 0; while (i < 100) { if (i == 5) { break; } s = s + i; i = i + 1; } return s; }", 10},
		{"continue-in-while", "function main(): i32 { var i = 0; var s = 0; while (i < 10) { i = i + 1; if (i == 5) { continue; } s = s + i; } return s; }", 50},
		// Mixed.
		{"mixed", "function main(): i32 { var a = 1 + 2; var b = 4 * 5; var c = a + b; if (c < 100) { return c; } return 0 - 1; }", 23},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.source))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}
