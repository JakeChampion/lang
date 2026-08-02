package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostTupleRet64bitIR is the correctness gate for functions that RETURN
// a tuple with i64 / f64 elements on the wasm IR backend. Such functions used to
// bail the whole module to the AST backend: tuple_elems_lowerable only admitted
// i32/bool/string/leaf-struct elements. The gate now admits i64/f64 too — tuple
// boxes already ride uniform 8-byte slots, and the call-site `.N` / destructure
// paths already recover the i64/f64 element width (op_tuple_get_w) from the
// tuple-return registry, so only the eligibility gate needed widening. Results
// pinned to hardcoded oracle values.
func TestSelfHostTupleRet64bitIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host tuple-ret-64bit wasm IR e2e")
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
		// Return (i64, i32); destructure binds a:i64. 2e10 > 1.5e10 -> 7
		{"ret-i64-destructure", `function mk(): (i64, i32) { return (20000000000, 3); } function main(): i32 { var (a, b) = mk(); if (a > 15000000000) { return 7; } return b; }`, 7},
		// Return (i64, i32); read .0 (i64) and .1 (i32) off the bound tuple.
		{"ret-i64-dotaccess", `function mk(): (i64, i32) { return (9000000000, 4); } function main(): i32 { var t = mk(); if (t.0 > 8000000000) { return t.1; } return 0; }`, 4},
		// Return (f64, i32); destructure binds x:f64. 2.5 > 2.0 -> 6
		{"ret-f64-destructure", `function mk(): (f64, i32) { return (2.5, 1); } function main(): i32 { var (x, n) = mk(); if (x > 2.0) { return 6; } return n; }`, 6},
		// Mixed (i64, f64): both 8-byte elements in one tuple return.
		{"ret-i64-f64-mixed", `function mk(): (i64, f64) { return (6000000000, 3.5); } function main(): i32 { var (a, x) = mk(); var ok = 0; if (a > 5000000000) { ok = ok + 5; } if (x > 3.0) { ok = ok + 4; } return ok; }`, 9},
		// i64 tuple element flows into arithmetic after destructure.
		{"ret-i64-arith", `function mk(): (i64, i64) { return (6000000000, 7000000000); } function main(): i32 { var (a, b) = mk(); var s: i64 = a + b; if (s > 12000000000) { return 8; } return 1; }`, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("tuple-ret-64bit wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
