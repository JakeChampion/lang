package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nestedClosureIRCases are nested capturing lambdas — a lambda whose body
// DEFINES another lambda. These bailed to the AST emitter for two reasons,
// both fixed here:
//
//  1. Capture analysis (astwalk.collect_idents_expr's ExprLambda case) recursed
//     into the inner lambda's body and collected the inner lambda's OWN params
//     (e.g. the `y` of `function(y){…}`) as references of the outer scope, so the
//     outer lambda's capture analysis saw an unresolvable capture and declined
//     the lift. It now contributes only the inner lambda's FREE variables.
//
//  2. lift_lambdas processed only the module's original functions once, so a
//     lambda lifted OUT of another lambda's body (the inner one) was never
//     re-examined. It now drives a worklist that re-processes every lifted
//     `__lam_N`, so nested lambdas lift in successive rounds. A two-level
//     capture resolves because the outer lift turns the captured outer var into
//     a PARAM the inner lift then sees.
var nestedClosureIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"no-outer-capture", `function main(): i32 { var f = function(x: i32): i32 { var g = function(y: i32): i32 { return y + 1; }; return g(x); }; return f(10); }`, 11},
	{"capture-outer-local", `function main(): i32 { var a = 1; var f = function(x: i32): i32 { var g = function(y: i32): i32 { return y + a; }; return g(x); }; return f(10); }`, 11},
	{"capture-outer-param", `function main(): i32 { var f = function(x: i32): i32 { var g = function(y: i32): i32 { return y + x; }; return g(5); }; return f(10); }`, 15},
	{"triple-nested", `function main(): i32 { var f = function(x: i32): i32 { var g = function(y: i32): i32 { var h = function(z: i32): i32 { return z + 1; }; return h(y); }; return g(x); }; return f(10); }`, 11},
}

// TestSelfHostNestedClosureX86IR gates nested capturing lambdas on x86-64: each
// case asserts the program routes through the "ir" path (via asm_pathprobe_run)
// and that the IR path computes the oracle exit code.
func TestSelfHostNestedClosureX86IR(t *testing.T) {
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

	for _, tc := range nestedClosureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if route != "ir" {
				t.Errorf("%s routed through %q path, want \"ir\"", tc.name, route)
			}
			if got := run(t, emit(t, tc.src)); got != tc.expected {
				t.Errorf("nested-closure x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
