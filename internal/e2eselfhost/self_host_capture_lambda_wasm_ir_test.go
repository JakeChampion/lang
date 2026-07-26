package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostCaptureLambdaWasmIR is the wasm gate for closures slice 2c (the
// x86 sibling is TestSelfHostCaptureLambdaX86IR): capturing lambdas bound to a
// local and used only as direct calls, lambda-lifted to __lam_<k> with captures
// threaded as arguments. Asserts the hardcoded oracle exit code AND that the
// program reaches the IR path (emits __lam_0). Exit codes are kept <= 125 (a
// wasm/WASI proc_exit constraint).
func TestSelfHostCaptureLambdaWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host capture-lambda wasm IR e2e")
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

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"single-capture", `function main(): i32 { var base: i32 = 20; var add = function(x: i32): i32 { return x + base; }; return add(5) + add(10); }`, 55},
		{"capture-param", `function f(base: i32): i32 { var g = function(x: i32): i32 { return x * base; }; return g(3) + g(4); } function main(): i32 { return f(10); }`, 70},
		{"multi-capture", `function main(): i32 { var a: i32 = 7; var b: i32 = 3; var combine = function(x: i32): i32 { return x + a - b; }; return combine(10); }`, 14},
		{"capture-in-loop", `function main(): i32 { var step: i32 = 2; var bump = function(x: i32): i32 { return x + step; }; var total: i32 = 0; var i: i32 = 0; while (i < 3) { total = bump(total); i = i + 1; } return total; }`, 6},
		// Unannotated literal captures: cap_type infers the type from an array /
		// struct LITERAL initializer (lit_init_type), so these lift like the
		// annotated/param cases (capture threaded as an ordinary typed argument).
		{"arr-literal-capture", `function main(): i32 { var a = [10, 20, 30]; var len = function(): i32 { return a.len(); }; return len(); }`, 3},
		{"arr-literal-index", `function main(): i32 { var a = [3, 5, 9]; var third = function(): i32 { return a[2]; }; return third(); }`, 9},
		{"strarr-literal-capture", `function main(): i32 { var a = ["x", "y"]; var len = function(): i32 { return a.len(); }; return len(); }`, 2},
		{"struct-literal-capture", `struct P { x: i32 } function main(): i32 { var p = P { x: 42 }; var get = function(): i32 { return p.x; }; return get(); }`, 42},
		// Nested capturing closure — inner captures the OUTER lambda's own capture
		// (`a` flows main → outer → inner). Before unwrap_sole_iife_return the
		// block-body `outer` lifted to `[return (IIFE)()]` with `inner` buried in
		// the IIFE, so it bailed to the wasm AST emitter which emitted `unknown
		// local $a` (a runtime trap). Now the IIFE is beta-reduced inline so inner
		// lifts and the module stays on the wasm IR path.
		{"nested-capture-transitive", `function main(): i32 { var a: i32 = 10; var outer = () => { var b: i32 = 20; var inner = () => a + b; inner() }; return outer(); }`, 30},
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
			if !strings.Contains(string(wat), "__lam_0") {
				t.Errorf("%q did not reach the IR path (no __lam_0 — lambda-lift bailed to AST)", tc.name)
			}
			watFile := filepath.Join(dir, "ir_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("capture-lambda wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
