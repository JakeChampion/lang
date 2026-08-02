package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostF64MethodsIR is the correctness gate for f64 methods (a method
// whose signature contains f64) on the wasm IR backend. These previously bailed
// the whole module to the AST path because f64_ret_fns_of recorded only free
// functions; it now records methods keyed "<Type>.<method>", and expr_is_f64's
// method case recovers the result's f64-ness. (i64 methods stay deferred until
// i64 struct fields land — an i64-field-reading method body needs struct_get_i64.)
// Results pinned to hardcoded oracle values.
func TestSelfHostF64MethodsIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host method-64bit wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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
		// f64 method: f64 param + f64 return. 3.0 * 2.5 = 7.5 > 7.0 -> 7
		{"f64-method-param-ret", `struct P { base: f64 } function (p: P) scaled(k: f64): f64 { return p.base * k; } function main(): i32 { var p = P { base: 3.0 }; var r: f64 = p.scaled(2.5); if (r > 7.0) { return 7; } return 0; }`, 7},
		// f64 method return, no f64 param: read an f64 field through a method.
		// 4.5 -> bound, > 4.0 -> 4
		{"f64-method-ret", `struct P { base: f64 } function (p: P) get(): f64 { return p.base; } function main(): i32 { var p = P { base: 4.5 }; var r: f64 = p.get(); if (r > 4.0) { return 4; } return 0; }`, 4},
		// f64 method with two f64 params + f64 field. (2.0 + 1.5) * 2.0 = 7.0; >6.5 -> 6
		{"f64-method-two-params", `struct P { base: f64 } function (p: P) f(a: f64, b: f64): f64 { return (p.base + a) * b; } function main(): i32 { var p = P { base: 2.0 }; var r: f64 = p.f(1.5, 2.0); if (r > 6.5) { return 6; } return 0; }`, 6},
		// f64 method returning a computed value used in a comparison chain.
		{"f64-method-chain", `struct V { x: f64 } function (v: V) half(): f64 { return v.x / 2.0; } function main(): i32 { var v = V { x: 9.0 }; var h: f64 = v.half(); if (h > 4.0) { return 3; } return 0; }`, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("method-64bit wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
