package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostLambdaX86IR is the x86-64 gate for closures slice 2a: a no-capture
// lambda passed directly as a call argument. irlower.lift_lambdas hoists it to a
// top-level __lam_<k> function and rewrites the argument to a bare reference, so
// it lowers through slice 1's const_func/call_indirect with no new IR ops. Pinned
// to hardcoded oracle exit codes via the asm_ir_run `-ir` path.
func TestSelfHostLambdaX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	runIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		innerAsm := filepath.Join(dir, "ir_inner.s")
		innerBin := filepath.Join(dir, "ir_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s\n--- asm ---\n%s", err, out, emitted)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally for %q", src)
		}
		return inner.ProcessState.ExitCode()
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
		{"var-bound-call", `function run(fn: () => i32): i32 { return fn(); } function main(): i32 { var n: i32 = run(function(): i32 { return 42; }); return n; }`, 42},
		// A capture-FREE lambda bound to a local and called directly: lifted to a
		// hoisted __lam_<k> and the call sites rewritten to direct calls (so it
		// lowers through the IR path instead of bailing to the AST closure box).
		{"local-bound-call", `function main(): i32 { var f = function(x: i32): i32 { return x * 2; }; return f(21); }`, 42},
		{"local-bound-twice", `function main(): i32 { var f = function(x: i32): i32 { return x + 1; }; return f(10) + f(30); }`, 42},
		{"two-local-lambdas", `function main(): i32 { var f = function(x: i32): i32 { return x + 1; }; var g = function(y: i32): i32 { return y * 3; }; return f(4) + g(5); }`, 20},
		{"local-calls-global", `function dbl(x: i32): i32 { return x * 2; } function main(): i32 { var f = function(x: i32): i32 { return dbl(x) + 1; }; return f(20); }`, 41},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("lambda x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
