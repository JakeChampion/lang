package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostFnArgInMatchIR pins the closure-lift fix for a fn-value passed as
// a call ARGUMENT inside a `match` SCRUTINEE (and an arm body). The lift pass
// (lift_inline_closures_stmts) env-boxes a bare fn-name arg into a `$wrap`
// trampoline so the callee — whose fn-param is a closure local — receives a box
// and dispatches env-first. It walked StmtIf/While/For conditions but had NO
// StmtMatch arm, so `match (g(arr, is_even)) { … }` left `is_even` a BARE fn
// pointer; the callee then unpacked a box from it and segfaulted on the
// indirect call. This is the latent IR bug that surfaced (#3457) once std/test
// modules routed IR — every test ends `match`-ing assertion results
// (wider_array_contains_count's `match (assert_count_i32(arr, is_even, n))`,
// map_eq's predicate). Forced through the IR path via the -ir driver.
func TestSelfHostFnArgInMatchIR(t *testing.T) {
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
			t.Fatalf("inner gcc: %v\n%s", err, out)
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

	// count_pred takes a fn-typed param and calls it (the closure-local call
	// convention). is_even is passed as a BARE fn-name argument inside a match
	// scrutinee; before the StmtMatch lift arm this passed an unwrapped pointer
	// and the indirect call segfaulted. Evens in [1..6] = 3 -> the `3` arm.
	const src = `function count_pred(arr: i32[], pred: (i32) => boolean): i32 {
    var h: i32 = 0; var i: i32 = 0;
    while (i < arr.len()) { if (pred(arr[i])) { h = h + 1; } i = i + 1; }
    return h;
}
function is_even(x: i32): boolean { return x % 2 == 0; }
function is_pos(x: i32): boolean { return x > 0; }
function main(): i32 {
    match (count_pred([1, 2, 3, 4, 5, 6], is_even)) {
        3 => {
            // a SECOND fn-value arg in an arm body must lift too.
            match (count_pred([1, 2, 3], is_pos)) { 3 => { return 42; }, _ => { return 1; }, }
        },
        _ => { return 2; },
    }
}`

	out := emit(t, src)
	if !strings.Contains(out, "$wrap") {
		t.Errorf("fn-value in match scrutinee did not reach the $wrap lift path (no $wrap in asm)")
	}
	if got := run(t, out); got != 42 {
		t.Errorf("fn-arg-in-match IR = %d, want 42", got)
	}
}
