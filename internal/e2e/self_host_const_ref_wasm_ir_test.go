package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostConstRefWasmIR is the wasm gate for bare `const` references (a
// zero-arg i32/boolean function, the desugared form of `const NAME = EXPR;`)
// lowering on the IR path: a value-position `NAME` becomes a direct call (arity
// 0). Eligibility is decided by the SHARED gate (asm_ir.eligible_core, used
// verbatim by wasm_eligible), and the x86 sibling (TestSelfHostConstRefX86IR)
// proves a const program reaches the IR path via a struct-free marker — wasm
// can't use an output marker because its IR backend emits byte-for-byte the same
// WAT as the reference AST backend (the differential / byte-equivalence
// contract). So this test runs each const program through the `-ir` driver and
// pins the oracle exit code, guarding the wasm const lowering against a
// width/op regression (which would diverge from the AST baseline and break the
// value). Exit codes are <= 125 (wasmtime's proc_exit ceiling).
func TestSelfHostConstRefWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host const-ref wasm IR e2e")
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
		// Two unannotated consts in arithmetic: 30 + 12 = 42.
		{"const-arith", `const A = 30; const B = 12; function main(): i32 { return A + B; }`, 42},
		// An annotated i32 const used through a helper: 7 + 7 = 14.
		{"typed-const", `const STEP: i32 = 7; function add_step(x: i32): i32 { return x + STEP; } function main(): i32 { return add_step(STEP); }`, 14},
		// A boolean const driving a branch.
		{"bool-const", `const ON = true; function main(): i32 { if (ON) { return 9; } return 1; }`, 9},
		// A const referenced inside a capturing closure body, alongside a
		// captured param: 3 + 5 + 100 = 108.
		{"const-in-closure", `const K = 100; function make_adder(n: i32): (i32) => i32 { return function(x: i32): i32 { return x + n + K; }; } function main(): i32 { var f = make_adder(5); return f(3); }`, 108},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("const-ref wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
