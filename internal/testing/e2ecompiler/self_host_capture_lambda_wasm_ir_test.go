package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostCaptureLambdaWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host capture-lambda wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"single-capture", `function main(): i32 { let base: i32 = 20; let add = (x: i32): i32 => { return x + base; }; return add(5) + add(10); }`, 55},
		{"capture-param", `function f(base: i32): i32 { let g = (x: i32): i32 => { return x * base; }; return g(3) + g(4); } function main(): i32 { return f(10); }`, 70},
		{"multi-capture", `function main(): i32 { let a: i32 = 7; let b: i32 = 3; let combine = (x: i32): i32 => { return x + a - b; }; return combine(10); }`, 14},
		{"capture-in-loop", `function main(): i32 { let step: i32 = 2; let bump = (x: i32): i32 => { return x + step; }; let total: i32 = 0; let i: i32 = 0; while (i < 3) { total = bump(total); i = i + 1; } return total; }`, 6},
		{"arr-literal-capture", `function main(): i32 { let a = [10, 20, 30]; let len = (): i32 => { return a.len(); }; return len(); }`, 3},
		{"arr-literal-index", `function main(): i32 { let a = [3, 5, 9]; let third = (): i32 => { return a[2]; }; return third(); }`, 9},
		{"strarr-literal-capture", `function main(): i32 { let a = ["x", "y"]; let len = (): i32 => { return a.len(); }; return len(); }`, 2},
		{"struct-literal-capture", `struct P { x: i32 } function main(): i32 { let p = P { x: 42 }; let get = (): i32 => { return p.x; }; return get(); }`, 42},
		{"nested-capture-transitive", `function main(): i32 { let a: i32 = 10; let outer = () => { let b: i32 = 20; let inner = () => a + b; inner() }; return outer(); }`, 30},
		{"i64-capture-inferred-ret", `function main(): i32 { let base: i64 = 7000000000; let f = () => base + 2000000000; return (f() / 1000000000) as i32; }`, 9},
		{"i64-param-inferred-ret", `function main(): i32 { let f = (x: i64) => x + 1; return f(5) as i32; }`, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
