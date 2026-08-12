package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostI64MethodsIR is the correctness gate for i64 methods (a method
// whose signature contains i64) on the wasm IR backend. The body reads an i64
// field (#2733) and i64_ret_fns_of records methods keyed "<Type>.<method>" as
// well as free functions, with the
// infer_expr_width / lower_i64 method cases recovering the result's i64-ness and
// param_is_i64 routing i64 args through lower_i64. Results pinned to oracle values.
func TestSelfHostI64MethodsIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host i64-methods wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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
		// i64 method returning an i64 field. c.get() = 2e10 > 1.5e10 -> 8
		{"ret-field", `struct C { base: i64 } function (c: C) get(): i64 { return c.base; } function main(): i32 { var c = C { base: 20000000000 }; var r: i64 = c.get(); if (r > 15000000000) { return 8; } return 0; }`, 8},
		// i64 method: i64 param + i64 return. base + x = 5e9 + 6e9 = 11e9 > 1e10 -> 5
		{"param-ret", `struct C { base: i64 } function (c: C) add(x: i64): i64 { return c.base + x; } function main(): i32 { var c = C { base: 5000000000 }; var r: i64 = c.add(6000000000); if (r > 10000000000) { return 5; } return 0; }`, 5},
		// i64 method param, i32 return (mixed): arg routed through lower_i64.
		// base(7e9) + x(6e9) = 1.3e10 > 1.2e10 -> 1
		{"param-i32ret", `struct C { base: i64 } function (c: C) over(x: i64): i32 { if (c.base + x > 12000000000) { return 1; } return 0; } function main(): i32 { var c = C { base: 7000000000 }; return c.over(6000000000); }`, 1},
		// i64 method used in a loop accumulating its returns.
		{"loop", `struct C { step: i64 } function (c: C) s(): i64 { return c.step; } function main(): i32 { var c = C { step: 3000000000 }; var acc: i64 = 0; var i = 0; while (i < 4) { acc = acc + c.s(); i = i + 1; } if (acc > 11000000000) { return 9; } return 0; }`, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("i64-method wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
