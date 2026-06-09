package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcClosureWasm proves Slice 1 of closure-env reclamation: a
// closure's env block is now rc-boxed (via $__fern_str_box, so table_idx@0 +
// captures@4+i*4 are unchanged) instead of raw-allocated, and an owned closure
// local (bound to a lambda literal) is FREED at function exit via the shared
// $__fern_arr_dec — reclaiming the env box that previously leaked entirely. A
// move-on-return closure is excluded from the sweep (handed to the caller).
// Captures still leak one level here (a later slice releases them); the box
// reclaim is sound on its own (detector 0). Cross-checks value + the
// over-release detector.
func TestSelfHostRcClosureWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("no wasmtime")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "wasm.fern", "wasm_run.fern"} {
		src, _ := os.ReadFile(filepath.Join("../../examples/self_host", name))
		os.WriteFile(filepath.Join(dir, name), src, 0o644)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	cases := []struct {
		name string
		src  string
		exit int
	}{
		// closure box freed at exit; value-correct + detector 0.
		{"clos-box-freed", "function main(): i32 { var n: i32 = 5; var f = function (x: i32): i32 { return x + n; }; return f(37) + __fern_rc_underflow_count(); }", 42},
		// multi-capture scalar closure freed; detector 0.
		{"clos-multi", "function main(): i32 { var a: i32 = 30; var b: i32 = 12; var f = function (): i32 { return a + b; }; return f() + __fern_rc_underflow_count(); }", 42},
		// string capture: box freed (capture leaks one level — sound), value ok.
		{"clos-string-cap", "function main(): i32 { var s: string = \"hello\"; var f = function (): i32 { return s.len(); }; return f() + 37 + __fern_rc_underflow_count(); }", 42},
		// churn: 200k scalar-capture closures built + dropped; boxes reclaim
		// each cycle (no OOM), detector 0 — proves real box free.
		{"clos-scalar-churn", "function mk(k: i32): i32 { var n: i32 = k; var f = function (x: i32): i32 { return x + n; }; return f(1); } function main(): i32 { var s = 0; var k = 0; while (k < 200000) { s = mk(k); k = k + 1; } return (s % 7) + __fern_rc_underflow_count(); }", 3},
		// returned closure (move-on-return) excluded from sweep; caller's
		// binding (call init) not swept — no double free, value-correct.
		{"clos-return", "function adder(a: i32): (i32) => i32 { var f = function (b: i32): i32 { return a + b; }; return f; } function main(): i32 { var add10 = adder(10); var add20 = adder(20); return add10(5) + add20(7) + __fern_rc_underflow_count(); }", 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			os.WriteFile(watPath, wat, 0o644)
			cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: exited %d want %d\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}
