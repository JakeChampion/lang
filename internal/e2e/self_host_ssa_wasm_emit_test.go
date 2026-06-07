package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSSAEmitWasm exercises the self-hosted SSA → wasm backend
// (examples/self_host/ssa_wasm.fern): the ssa_wasm_emit_run driver parses a
// program, lowers each function to SSA, optimises it, and prints a WASI core
// module in text format (WAT). For each case the test validates the WAT
// (wasm-tools, when present) and runs it with `wasmtime run`, asserting the
// process exit code equals the program's value — end-to-end proof that the
// full self-hosted pipeline (AST → SSA → optimise → WAT → execute) is
// correct on the wasm backend, making wasm the third consumer of the shared
// SSA IR (after x86-64 and arm64).
//
// Scope mirrors ssa_wasm.fern's core integer subset (const / copy / binary /
// unary / call / phi over ret / br / brif); heap-backed values (arrays /
// strings / structs / maps / closures) are a later slice. The cases here are
// a subset of TestSelfHostSSAEmitX86_64's matrix that stays in that subset.
func TestSelfHostSSAEmitWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host SSA→wasm e2e")
	}
	gcc, _ := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "ssa.fern", "ssa_wasm.fern", "ssa_wasm_emit_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	bin := buildSelfHostBin(t, gcc, dir, "ssa_wasm_emit_run.fern", "ssa_wasm_emit_run")
	wasmtools, _ := exec.LookPath("wasm-tools")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"const", "function main(): i32 { return 42; }", 42},
		{"arith", "function main(): i32 { return 2 + 3 * 4; }", 14},
		{"parens", "function main(): i32 { return (1 + 2) * 3; }", 9},
		{"locals", "function main(): i32 { var x = 10; var y = x - 3; return y * 2; }", 14},
		{"division", "function main(): i32 { return 84 / 2; }", 42},
		{"modulo", "function main(): i32 { return 23 % 5; }", 3},
		{"bitwise", "function main(): i32 { return (6 & 3) | 8; }", 10},
		{"shift", "function main(): i32 { return 1 << 4; }", 16},
		{"xor", "function main(): i32 { return 12 ^ 10; }", 6},
		{"comparison", "function main(): i32 { return 7 > 3; }", 1},
		{"logical-and", "function main(): i32 { var a = 1; var b = 0; if (a > 0 && b == 0) { return 1; } return 0; }", 1},
		{"logical-or", "function main(): i32 { var a = 0; if (a > 5 || a == 0) { return 1; } return 0; }", 1},
		{"unary-not", "function main(): i32 { var x = 5; if (!(x > 9)) { return 1; } return 0; }", 1},
		{"unary-neg", "function main(): i32 { var x = 0 - 7; return 0 - x; }", 7},
		{"if-else", "function main(): i32 { var x = 0; if (5 < 10) { x = 7; } else { x = 9; } return x; }", 7},
		{"early-return", "function main(): i32 { var x = 5; if (x > 3) { return 100; } return x; }", 100},
		{"nested-if", "function main(): i32 { var x = 5; if (x > 0) { if (x > 3) { x = 100; } else { x = 50; } } return x; }", 100},
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }", 15},
		{"while-factorial", "function main(): i32 { var i = 1; var f = 1; while (i <= 5) { f = f * i; i = i + 1; } return f; }", 120},
		{"if-in-loop", "function main(): i32 { var i = 0; var c = 0; while (i < 10) { if (i > 4) { c = c + 1; } i = i + 1; } return c; }", 5},
		{"nested-loop", "function main(): i32 { var i = 0; var t = 0; while (i < 3) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }", 9},
		{"break", "function main(): i32 { var i = 0; while (i < 100) { if (i == 5) { break; } i = i + 1; } return i; }", 5},
		{"continue", "function main(): i32 { var i = 0; var s = 0; while (i < 10) { i = i + 1; if (i == 5) { continue; } s = s + i; } return s; }", 50},
		// Multi-function: argument passing + call/return + recursion.
		{"call", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(3, 4); }", 7},
		{"call-expr", "function sq(x: i32): i32 { return x * x; } function main(): i32 { return sq(5) + sq(3); }", 34},
		{"recursion", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
		{"fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { var s = 0; var i = 0; while (i < 10) { s = s + fib(i); i = i + 1; } return s; }", 88},
		{"all-return-helper", "function sign(n: i32): i32 { if (n < 0) { return 0 - 1; } else if (n == 0) { return 0; } else { return 1; } } function main(): i32 { return sign(0 - 5) + 10 * sign(7); }", 9},
		// No-capture lambdas lift to top-level functions and are called directly.
		{"lambda-call", "function main(): i32 { var f = function (x: i32): i32 { return x + 1; }; return f(5); }", 6},
		{"lambda-compose", "function main(): i32 { var inc = function (x: i32): i32 { return x + 1; }; var dbl = function (x: i32): i32 { return x * 2; }; return inc(dbl(10)); }", 21},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			emit := exec.Command(bin)
			emit.Stdin = strings.NewReader(tc.src)
			wat, err := emit.Output()
			if err != nil {
				t.Fatalf("emit driver failed for %q: %v", tc.src, err)
			}
			watPath := filepath.Join(dir, "prog.wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			if wasmtools != "" {
				if out, err := exec.Command(wasmtools, "validate", watPath).CombinedOutput(); err != nil {
					t.Fatalf("wasm-tools validate failed for %q: %v\n%s\n--- WAT ---\n%s", tc.src, err, out, wat)
				}
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("wasm program did not exit normally for %q", tc.src)
			}
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("SSA→wasm of %q = %d, want %d\n--- WAT ---\n%s", tc.src, got, tc.want, wat)
			}
		})
	}
}
