package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostErrDeferIR exercises `errdefer` through the SELF-HOSTED x86-64
// compiler, both the AST path and the IR path (the asm_ir_run driver's `-ir`
// flag). `errdefer` is a parse-time desugar (parser.lower_defers_module, run in
// module_with_builtins before either backend), so it must work — and agree —
// on both paths.
//
// errdefer cleanup fires only on an error exit. The self-host desugar — like
// the self-host's plain `defer` — detects the explicit `return None` / `return
// Err(...)` form (the `?` operator is lowered later, in the emitter, so it is
// out of this pass's scope; the native compiler covers the `?` path). Side
// effects are observed through a 1-element i32[] used as a mutable cell, so the
// whole contract is encoded in the process exit code.
func TestSelfHostErrDeferIR(t *testing.T) {
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
	// Build the asm_ir_run driver once via the production x86-64 backend.
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
			t.Fatalf("inner gcc (ir=%v): %v\n%s\n--- asm ---\n%s", ir, err, out, emitted)
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
		// Result: errdefer fires on `return Err`, not on `return Ok`.
		{"success_no_fire", `function f(out: i32[], x: i32): Result[i32, i32] { errdefer out[0] = 9; if (x < 0) { return Err(1); } return Ok(x); } function main(): i32 { var a: i32[] = [0]; match (f(a, 5)) { Ok(v) => {}, Err(e) => {} } return a[0]; }`, 0},
		{"err_return_fires", `function f(out: i32[], x: i32): Result[i32, i32] { errdefer out[0] = 9; if (x < 0) { return Err(1); } return Ok(x); } function main(): i32 { var a: i32[] = [0]; match (f(a, -1)) { Ok(v) => {}, Err(e) => {} } return a[0]; }`, 9},
		// Option: fires on `return None`, not on `return Some`.
		{"option_none_fires", `function opt(out: i32[], x: i32): Option[i32] { errdefer out[0] = 5; if (x < 0) { return None; } return Some(x); } function main(): i32 { var a: i32[] = [0]; match (opt(a, -1)) { Some(v) => {}, None => {} } return a[0]; }`, 5},
		{"option_some_no_fire", `function opt(out: i32[], x: i32): Option[i32] { errdefer out[0] = 5; if (x < 0) { return None; } return Some(x); } function main(): i32 { var a: i32[] = [0]; match (opt(a, 7)) { Some(v) => {}, None => {} } return a[0]; }`, 0},
		// defer runs on every exit; errdefer only on the error exit, after the
		// defer. Success: 0 +1 (defer) = 1. Error: 0 +1 (defer) +10 (errdefer) = 11.
		{"defer_and_errdefer_success", `function h(out: i32[], x: i32): Result[i32, i32] { defer out[0] = out[0] + 1; errdefer out[0] = out[0] + 10; if (x < 0) { return Err(1); } return Ok(x); } function main(): i32 { var a: i32[] = [0]; match (h(a, 5)) { Ok(v) => {}, Err(e) => {} } return a[0]; }`, 1},
		{"defer_and_errdefer_error", `function h(out: i32[], x: i32): Result[i32, i32] { defer out[0] = out[0] + 1; errdefer out[0] = out[0] + 10; if (x < 0) { return Err(1); } return Ok(x); } function main(): i32 { var a: i32[] = [0]; match (h(a, -1)) { Ok(v) => {}, Err(e) => {} } return a[0]; }`, 11},
		// Conditionally-reached errdefer: only fires when its statement ran.
		{"conditional_registered_fires", `function cond(out: i32[], reg: boolean, x: i32): Result[i32, i32] { if (reg) { errdefer out[0] = 7; } if (x < 0) { return Err(1); } return Ok(x); } function main(): i32 { var a: i32[] = [0]; match (cond(a, true, -1)) { Ok(v) => {}, Err(e) => {} } return a[0]; }`, 7},
		{"conditional_unregistered_no_fire", `function cond(out: i32[], reg: boolean, x: i32): Result[i32, i32] { if (reg) { errdefer out[0] = 7; } if (x < 0) { return Err(1); } return Ok(x); } function main(): i32 { var a: i32[] = [0]; match (cond(a, false, -1)) { Ok(v) => {}, Err(e) => {} } return a[0]; }`, 0},
		// Two errdefers fire LIFO on error: out = out*10 + id gives 21.
		{"lifo_order", `function m(out: i32[], x: i32): Result[i32, i32] { errdefer out[0] = out[0] * 10 + 1; errdefer out[0] = out[0] * 10 + 2; if (x < 0) { return Err(1); } return Ok(x); } function main(): i32 { var a: i32[] = [0]; match (m(a, -1)) { Ok(v) => {}, Err(e) => {} } return a[0]; }`, 21},
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
