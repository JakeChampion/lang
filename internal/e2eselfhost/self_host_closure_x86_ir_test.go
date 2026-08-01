package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostClosureX86IR is the x86-64 gate for closures slice 3a: first-class
// (escaping) capturing closures — a `return function(x){ … cap … }` returned as
// a value, bound to a local, and called. lift_lambdas hoists the body to
// `<fn>$clo(__env, params…)`; irlower lowers the lambda to an i32[] env box
// [funcval, caps…] (make_closure via const_func + arr_make), and a call through
// the closure local loads box[0] and dispatches env-first via call_indirect.
// i32 captures only. Each case asserts the oracle exit code AND that the IR path
// was taken (`$clo` in the emitted asm).
func TestSelfHostClosureX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
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
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		return string(out)
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
		{"adder", `function make_adder(n: i32): (i32) => i32 { return function(x: i32): i32 { return x + n; }; } function main(): i32 { var add5 = make_adder(5); return add5(3); }`, 8},
		{"multi-capture", `function make(a: i32, b: i32): (i32) => i32 { return function(x: i32): i32 { return x * a + b; }; } function main(): i32 { var f = make(3, 7); return f(5); }`, 22},
		{"called-twice", `function make(a: i32, b: i32): (i32) => i32 { return function(x: i32): i32 { return x * a + b; }; } function main(): i32 { var f = make(2, 1); return f(3) + f(4); }`, 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := emit(t, tc.src)
			if !strings.Contains(out, "$clo") {
				t.Errorf("%q did not reach the IR closure path (no $clo in asm)", tc.name)
			}
			if got := run(t, out); got != tc.expected {
				t.Errorf("closure x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
