package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostCaptureLambdaX86IR is the x86-64 gate for closures slice 2c:
// capturing lambdas bound to a local and used only as direct calls. They are
// lambda-lifted — hoisted to __lam_<k>(origparams…, captures…) with each call
// rewritten to thread the captured values as arguments — so no closure box is
// needed and the existing direct-call IR path lowers them. Each case asserts the
// hardcoded oracle exit code AND (separately) that the program reaches the IR
// path (emits __lam_0), not the AST fallback.
func TestSelfHostCaptureLambdaX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	emit := func(t *testing.T, src string) string {
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
		return string(emitted)
	}
	run := func(t *testing.T, asmText string) int {
		t.Helper()
		innerAsm := filepath.Join(dir, "ir_inner.s")
		innerBin := filepath.Join(dir, "ir_inner")
		if err := os.WriteFile(innerAsm, []byte(asmText), 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s\n--- asm ---\n%s", err, out, asmText)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally")
		}
		return inner.ProcessState.ExitCode()
	}

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"single-capture", `function main(): i32 { var base: i32 = 20; var add = function(x: i32): i32 { return x + base; }; return add(5) + add(10); }`, 55},
		{"capture-param", `function f(base: i32): i32 { var g = function(x: i32): i32 { return x * base; }; return g(3) + g(4); } function main(): i32 { return f(10); }`, 70},
		{"multi-capture", `function main(): i32 { var a: i32 = 7; var b: i32 = 3; var combine = function(x: i32): i32 { return x + a - b; }; return combine(10); }`, 14},
		{"capture-in-loop", `function main(): i32 { var step: i32 = 2; var bump = function(x: i32): i32 { return x + step; }; var total: i32 = 0; var i: i32 = 0; while (i < 3) { total = bump(total); i = i + 1; } return total; }`, 6},
		// Unannotated literal captures: cap_type now infers the type from an
		// array / struct LITERAL initializer (lit_init_type), so `var a =
		// [..]` / `var p = P{..}` captures lift like the annotated/param cases
		// (the capture flows as an ordinary typed argument — no env box, no RC).
		{"arr-literal-capture", `function main(): i32 { var a = [10, 20, 30]; var len = function(): i32 { return a.len(); }; return len(); }`, 3},
		{"arr-literal-index", `function main(): i32 { var a = [3, 5, 9]; var third = function(): i32 { return a[2]; }; return third(); }`, 9},
		{"strarr-literal-capture", `function main(): i32 { var a = ["x", "y"]; var len = function(): i32 { return a.len(); }; return len(); }`, 2},
		{"struct-literal-capture", `struct P { x: i32 } function main(): i32 { var p = P { x: 42 }; var get = function(): i32 { return p.x; }; return get(); }`, 42},
		// Wide-value (i64/u64) lambdas with an INFERRED (empty) return type: the
		// lifted __lam_N carried ret_type "" so eligibility's `lower` bailed on the
		// i64 return (ret_is_i64 unknown) → the lambda routed to the AST emitter.
		// lift_lambdas now infers the lifted body's return type, so these lower on
		// the IR path. (An explicit `function(x: i64): i64` return already worked;
		// the gap was the arrow / inferred-return form.)
		{"i64-capture-inferred-ret", `function main(): i32 { var base: i64 = 7000000000; var f = () => base + 2000000000; return (f() / 1000000000) as i32; }`, 9},
		{"i64-param-inferred-ret", `function main(): i32 { var f = (x: i64) => x + 1; return f(5) as i32; }`, 6},
		{"u64-param-inferred-ret", `function main(): i32 { var f = (x: u64) => x + 3; return f(5) as i32; }`, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := emit(t, tc.src)
			if !strings.Contains(out, "__lam_0") {
				t.Errorf("%q did not reach the IR path (no __lam_0 — lambda-lift bailed to AST)", tc.name)
			}
			if got := run(t, out); got != tc.expected {
				t.Errorf("capture-lambda x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
