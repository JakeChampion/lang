package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostTupleReuseWasmIR proves the tuple constructor-reuse (FBIP) path
// lowers correctly on the self-hosted WASM IR backend, where the per-element
// store WIDTH matters (unlike the register backends' uniform 8-byte slots): the
// reuse writer (`op_tuple_set` / `_w`) must emit i64.store for an i64 element,
// f64.store for an f64 element, and i32.store for an i32 / pointer element — the
// same instruction selection `op_tuple_make_k` uses for a fresh construction. A
// wrong store width (e.g. i32.store into an i64 slot) truncates the value and the
// later i64.load reads garbage, so the exit code pins width correctness.
//
// The recipient tuple `b` reuses the dead donor `a`'s box in place (verified on
// x86-64 by the box-count assertion in TestSelfHostTupleReuseIRX86_64); here the
// value-through-wasm is the contract. Exit codes are kept < 126 to stay inside
// WASI's valid _start exit range.
func TestSelfHostTupleReuseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host tuple-reuse wasm IR e2e")
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

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// i32 elements: reused box overwritten with i32.store. (5+7) + (2+3) = 17.
		{"reuse-i32", `function main(): i32 { var a: (i32, i32) = (5, 7); var s: i32 = a.0 + a.1; var b: (i32, i32) = (2, 3); return s + b.0 + b.1; }`, 17},
		// i64 elements: 8-byte i64.store into the reused box; an i32.store would
		// truncate and the i64.load read would be garbage. (5+7) + (30+20) = 62.
		{"reuse-i64", `function main(): i32 { var a: (i64, i64) = (5, 7); var s: i64 = a.0 + a.1; var b: (i64, i64) = (30, 20); return (s + b.0 + b.1) as i32; }`, 62},
		// f64 elements: 8-byte f64.store. (1.5+2.5) + (10.0+3.0) = 17.
		{"reuse-f64", `function main(): i32 { var a: (f64, f64) = (1.5, 2.5); var s: f64 = a.0 + a.1; var b: (f64, f64) = (10.0, 3.0); return (s + b.0 + b.1) as i32; }`, 17},
		// Mixed i32 + i64: element 0 stored with i32.store, element 1 with i64.store
		// — each at its own width in the same reused box. (5+7) + (2+30) = 44.
		{"reuse-mixed", `function main(): i32 { var a: (i32, i64) = (5, 7); var s: i64 = (a.0 as i64) + a.1; var b: (i32, i64) = (2, 30); return (s + (b.0 as i64) + b.1) as i32; }`, 44},
		// String (pointer) element: i32.store of the pointer into the reused box.
		// a.1(5) → s=5; b=("yo",9); 5 + 9 + len("yo")=2 = 16.
		{"reuse-string", `function main(): i32 { var a: (string, i32) = ("hi", 5); var s: i32 = a.1; var b: (string, i32) = ("yo", 9); return s + b.1 + b.0.len(); }`, 16},
		// Donor still live (read after b is built): reuse suppressed, both tuples
		// allocate independently, value stays correct. (5+7) + (2+3) = 17.
		{"donor-live", `function main(): i32 { var a: (i32, i32) = (5, 7); var b: (i32, i32) = (2, 3); return a.0 + a.1 + b.0 + b.1; }`, 17},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !strings.Contains(string(wat), "$__fern_str_box") {
				t.Errorf("%q did not reach the IR tuple path (no tuple box in WAT)", tc.name)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("tuple reuse wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
