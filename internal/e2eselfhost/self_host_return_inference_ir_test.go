package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostReturnInferenceIR exercises return-type inference through the
// SELF-HOSTED x86-64 compiler, on both the AST path and the IR path
// (asm_ir_run `-ir`). The self-host infers an unannotated function's return
// type from its `return` expressions (parser.infer_ret_types_module, run in
// module_with_builtins for the AST path and at the top of emit_module_ir for
// the IR path), so a call site tags the result correctly — e.g. `greet()`
// must be known to return string for `.len()` to dispatch.
//
// i32 returns already worked before inference (i32 is the default tag); the
// string / Option / call-chain cases are the ones the inference fixes, and
// the AST and IR paths must agree.
func TestSelfHostReturnInferenceIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	emitAndRun := func(t *testing.T, src string, ir bool) int {
		t.Helper()
		args := []string{}
		if ir {
			args = append(args, "-ir")
		}
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed (ir=%v) for %q: %v", ir, src, err)
		}
		tag := "ast"
		if ir {
			tag = "ir"
		}
		innerAsm := filepath.Join(dir, tag+"_inner.s")
		innerBin := filepath.Join(dir, tag+"_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc (ir=%v): %v\n%s", ir, err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally (ir=%v) for %q", ir, src)
		}
		return inner.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"i32-add", `function add(a: i32, b: i32) { return a + b; } function main(): i32 { return add(40, 2); }`, 42},
		{"string-len", `function greet() { return "hi"; } function main(): i32 { return greet().len(); }`, 2},
		{"string-via-call", `function chain() { return inner(); } function inner() { return "abcd"; } function main(): i32 { return chain().len(); }`, 4},
		{"bool", `function pos(n: i32) { return n > 0; } function main(): i32 { if (pos(5)) { return 7; } return 0; }`, 7},
		{"branches", `function pick(b: boolean) { if (b) { return 10; } return 20; } function main(): i32 { return pick(true) + pick(false); }`, 30},
		{"option-some", `function find(n: i32) { if (n > 0) { return Some(n); } return None; } function main(): i32 { match (find(5)) { Some(v) => { return v; }, None => { return 0; } } return 9; }`, 5},
		{"option-none", `function find(n: i32) { if (n > 0) { return Some(n); } return None; } function main(): i32 { match (find(-1)) { Some(v) => { return v; }, None => { return 8; } } return 9; }`, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			astCode := emitAndRun(t, tc.src, false)
			irCode := emitAndRun(t, tc.src, true)
			if astCode != irCode {
				t.Errorf("AST-path vs IR-path mismatch for %q: AST=%d IR=%d", tc.name, astCode, irCode)
			}
			if irCode != tc.want {
				t.Errorf("self-host IR path %q: exit = %d, want %d", tc.name, irCode, tc.want)
			}
		})
	}
}
