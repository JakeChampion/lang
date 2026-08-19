package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostLambdaWasmIR is the wasm gate for closures slice 2a (the x86
// sibling is TestSelfHostLambdaX86IR): a no-capture lambda passed directly as a
// call argument. irlower.lift_lambdas hoists it to a top-level __lam_<k> and the
// argument becomes a bare reference, so it lowers through slice 1's
// const_func/call_indirect funcref table. Pinned to hardcoded oracle exit codes.
func TestSelfHostLambdaWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host lambda wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
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
		{"apply", `function apply(f: (i32) => i32, v: i32): i32 { return f(v); } function main(): i32 { return apply(function(x: i32): i32 { return x + 1; }, 41); }`, 42},
		{"predicate", `function count_if(arr: i32[], pred: (i32) => boolean): i32 { var c: i32 = 0; for x in arr { if (pred(x)) { c = c + 1; } } return c; } function main(): i32 { var a: i32[] = [5, 20, 8, 30, 15]; return count_if(a, function(n: i32): boolean { return n > 10; }); }`, 3},
		{"calls-global", `function dbl(x: i32): i32 { return x * 2; } function apply(f: (i32) => i32, v: i32): i32 { return f(v); } function main(): i32 { return apply(function(x: i32): i32 { return dbl(x) + 1; }, 20); }`, 41},
		{"two-arg", `function run2(g: (i32, i32) => i32, p: i32, q: i32): i32 { return g(p, q); } function main(): i32 { return run2(function(x: i32, y: i32): i32 { return x * 10 + y; }, 4, 2); }`, 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("lambda wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
