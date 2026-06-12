package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
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
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	prog, _, err := modload.Load(filepath.Join(dir, "asm_ir_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverAsm := filepath.Join(dir, "driver.s")
	driverBin := filepath.Join(dir, "driver")
	if err := os.WriteFile(driverAsm, []byte(asm), 0o644); err != nil {
		t.Fatalf("write driver asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", driverAsm, "-o", driverBin).CombinedOutput(); err != nil {
		t.Fatalf("driver gcc: %v\n%s", err, out)
	}

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
