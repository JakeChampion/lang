package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostLambdaReachesIR guards that lambda programs actually lower
// through the IR path, not the AST fallback. The IR path never emits the AST
// tagged-value runtime (release_JNull / …), and each lambda position has a
// designed IR lowering whose artifacts the AST backend never produces:
//
//   - A no-capture lambda passed as a fn-typed CALL ARGUMENT rides the uniform
//     env-box ABI (irlower.lift_inline_closures_expr): it is wrapped into an
//     env-ignoring `<fn>$wrapN` trampoline boxed as [funcval], and the callee
//     dispatches it env-first via call_indirect. This pass deliberately runs
//     BEFORE the no-capture __lam_N lift ("there is one fn-arg shape, the
//     box"), so a lambda argument never becomes a bare __lam_N — the guard
//     here is the trampoline + call_indirect, not __lam_0.
//   - A capture-free lambda BOUND TO A LOCAL and called directly is hoisted to
//     a top-level __lam_<k> and the call rewritten to a direct call
//     (irlower.lift_stmt).
//
// This is the check that was missing while the lambda slices silently rode the
// AST fallback: a native free-list bug corrupted the lift's reconstructed
// statements, the lifted module bailed IR eligibility, and the exit-code-only
// tests passed via AST. The fix (binding each rebuilt statement to a `var`
// before the result array) is in irlower.lift_stmt.
func TestSelfHostLambdaReachesIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping lambda-reaches-IR guard")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	// A no-capture lambda passed as a callback — the uniform env-box shape.
	src := `function apply(f: (i32) => i32, v: i32): i32 { return f(v); } function main(): i32 { return apply(function(x: i32): i32 { return x + 1; }, 41); }`
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	out := string(wat)
	if !strings.Contains(out, "$main$wrap0") {
		t.Errorf("lambda argument did not reach the IR env-box path: emitted WAT has no $main$wrap0 trampoline\n%s", out)
	}
	if !strings.Contains(out, "call_indirect") {
		t.Errorf("lambda argument did not reach the IR env-box path: emitted WAT has no call_indirect dispatch\n%s", out)
	}
	if strings.Contains(out, "release_JNull") {
		t.Errorf("lambda program fell back to the AST backend (AST tagged-value runtime present)")
	}

	// A capture-free lambda BOUND TO A LOCAL and called directly must also
	// reach the IR path: the lift hoists it to __lam_<k> and rewrites `f(a)` to
	// a direct call, rather than bailing to the AST closure box. (Before, only
	// lambdas in argument position were lifted.)
	src2 := `function main(): i32 { var f = function(x: i32): i32 { return x * 2; }; return f(21); }`
	var cmd2 *exec.Cmd
	if len(runner) == 0 {
		cmd2 = exec.Command(driverBin, "-ir")
	} else {
		cmd2 = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd2.Stdin = bytes.NewReader([]byte(src2))
	wat2, err := cmd2.Output()
	if err != nil || len(wat2) == 0 {
		t.Fatalf("driver failed (local-bound lambda): %v", err)
	}
	out2 := string(wat2)
	if !strings.Contains(out2, "__lam_0") {
		t.Errorf("local-bound lambda did not reach the IR path: emitted WAT has no __lam_0 (lifted fn)\n%s", out2)
	}
	if strings.Contains(out2, "release_JNull") {
		t.Errorf("local-bound lambda fell back to the AST backend (AST tagged-value runtime present)")
	}
}
