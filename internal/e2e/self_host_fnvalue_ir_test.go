package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostFnValueIR is the correctness gate for plain (capture-free)
// function VALUES on the wasm IR backend — the first closures slice. A bare
// top-level function used as a value lowers to const_func (a funcref-table
// index), a call through a "fn"-typed local/param lowers to call_indirect, and
// the module grows a $fn<N> signature type + (table)/(elem) segment
// (fn_support_section). The register backends still bail such modules to AST
// (all_eligible keeps the !module_uses_fn_values restriction), so this is
// wasm-only. Results pinned to hardcoded oracle exit codes.
func TestSelfHostFnValueIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fn-value wasm IR e2e")
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
		// Pass a 0-arg function by name, call it through the "fn" param.
		{"value-call", `function work(): i32 { return 42; } function run(fn: () => i32): i32 { return fn(); } function main(): i32 { return run(work); }`, 42},
		// Pass a 1-arg function and call it with an argument.
		{"value-arg", `function inc(x: i32): i32 { return x + 1; } function apply(f: (i32) => i32, v: i32): i32 { return f(v); } function main(): i32 { return apply(inc, 41); }`, 42},
		// A predicate over an array: function value + array + for-in all lower.
		{"predicate", `function count_if(arr: i32[], pred: (i32) => boolean): i32 { var c: i32 = 0; for x in arr { if (pred(x)) { c = c + 1; } } return c; } function is_big(n: i32): boolean { return n > 10; } function main(): i32 { var a: i32[] = [5, 20, 8, 30, 15]; return count_if(a, is_big); }`, 3},
		// A function-valued local, reassigned, then dispatched — two table slots.
		{"value-local-reassign", `function a(): i32 { return 10; } function b(): i32 { return 20; } function pick(which: i32): i32 { var f = a; if (which > 0) { f = b; } return f(); } function main(): i32 { return pick(1); }`, 20},
		// Two distinct arities dispatched in one module ($fn0 and $fn1 types).
		{"mixed-arity", `function z(): i32 { return 5; } function s(x: i32): i32 { return x * 2; } function run0(f: () => i32): i32 { return f(); } function run1(g: (i32) => i32, n: i32): i32 { return g(n); } function main(): i32 { return run0(z) + run1(s, 18); }`, 41},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("fn-value wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
