package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// lambdaStructFieldIRCases place a lambda LITERAL in a STRUCT-FIELD value
// position (the array-element case landed separately in #3036). A bare named
// function value already lowered there (`[inc]`, `Box { f: inc }` → const_func),
// but a lambda literal did not: the lift walk (lift_expr_walk) only recursed
// into call-argument positions, so a lambda inside an array / struct literal was
// never hoisted and the module bailed to the AST emitter. lift_expr_walk now
// recurses into ExprArray and ExprStructLit, hoisting each no-capture lambda
// element to a top-level __lam_N (leaving a bare fn-name → const_func), exactly
// like a no-capture call arg.
//
// A CAPTURING lambda in a struct field (`Box { f: function(x){ return x + n; } }`)
// is the env-box fn-value shape from #3445 — the capturing lambda hoists to a
// `$cloN` env box `[funcval, caps…]` and `(b.f)(x)` dispatches env-first. The
// `capturing-*` cases below pin it (it was the last of #3445's three shapes
// — struct field / call arg / map value — to lack value-pinned coverage; the
// other two are covered by self_host_asm_ir_path_test.go's `clo-cap-fn-arg`
// family and self_host_closure_*_ir_test.go's `map-value-closure-captured`).
var lambdaStructFieldIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"one-field", `struct Box { f: (i32) => i32 } function main(): i32 { var b = Box { f: function(x: i32): i32 { return x + 1; } }; return (b.f)(10); }`, 11},
	{"two-fields", `struct Ops { inc: (i32) => i32, dbl: (i32) => i32 } function main(): i32 { var o = Ops { inc: function(x: i32): i32 { return x + 1; }, dbl: function(x: i32): i32 { return x * 2; } }; return (o.inc)(10) + (o.dbl)(10); }`, 31},
	{"mixed-scalar-and-fn", `struct Wrap { n: i32, f: (i32) => i32 } function main(): i32 { var w = Wrap { n: 5, f: function(x: i32): i32 { return x * 3; } }; return w.n + (w.f)(10); }`, 35},
	// #3445 case 1: a CAPTURING lambda in a struct field (captures the local `n`).
	{"capturing-field", `struct Box { f: (i32) => i32 } function main(): i32 { var n = 10; var b = Box { f: function(x: i32): i32 { return x + n; } }; return (b.f)(7); }`, 17},
	// Two captured locals in the struct-field closure.
	{"capturing-two-caps", `struct Box { f: (i32) => i32 } function main(): i32 { var a = 8; var b = 100; var bx = Box { f: function(x: i32): i32 { return x + a + b; } }; return (bx.f)(0); }`, 108},
	// A capturing closure field alongside a scalar field (the env box and the
	// i32 field coexist in the struct).
	{"capturing-mixed-field", `struct Wrap { n: i32, f: (i32) => i32 } function main(): i32 { var k = 3; var w = Wrap { n: 5, f: function(x: i32): i32 { return x * k; } }; return w.n + (w.f)(10); }`, 35},
}

// TestSelfHostLambdaStructFieldX86IR gates no-capture lambda literals stored in an
// array / struct on x86-64: each case asserts the program routes through the
// "ir" path (via asm_pathprobe_run) and that the IR path computes the oracle.
func TestSelfHostLambdaStructFieldX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)

	probeSrc, err := os.ReadFile("../../examples/self_host/asm_pathprobe_run.fern")
	if err != nil {
		t.Fatalf("read asm_pathprobe_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_pathprobe_run.fern"), probeSrc, 0o644); err != nil {
		t.Fatalf("write asm_pathprobe_run.fern: %v", err)
	}
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "asm_ir_run.fern")
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

	for _, tc := range lambdaStructFieldIRCases {
		t.Run(tc.name, func(t *testing.T) {
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if route != "ir" {
				t.Errorf("%s routed through %q path, want \"ir\"", tc.name, route)
			}
			if got := run(t, emit(t, tc.src)); got != tc.expected {
				t.Errorf("lambda-structfield x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
