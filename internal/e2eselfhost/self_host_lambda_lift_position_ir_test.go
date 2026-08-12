package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// lambdaLiftPositionIRCases exercise no-capture lambdas in three more positions
// that the lambda-lift pre-pass now hoists to top-level `__lam_N` functions, so
// they lower through the self-host IR path on x86-64 + wasm:
//
//   - IIFE callee: `(function(b){...})(args)` -> `__lam_N(args)` (a direct call).
//   - tuple element: `(function(x){...}, 10)` -> a fn-pointer tuple element, so
//     `t.0(t.1)` rides the tuple-element call_indirect path.
//   - assignment RHS: `f = function(x){...}` -> `f = __lam_N` (a fn-pointer store).
//
// `lift_lambdas` already hoisted no-capture lambdas in call-argument /
// array-element / struct-field / return positions; these add the IIFE-callee,
// tuple-element, and assignment-RHS positions (lift_call_arg in lift_expr_walk's
// ExprCall callee + new ExprTuple arm, and in lift_stmt's StmtAssign arm). A
// CAPTURING lambda in any of these positions is left in place (still bails to
// AST), since calling it needs the env-passing closure form.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir",
// and returns a value <= 126 (wasmtime exit-code truncation, cf. #2908).
var lambdaLiftPositionIRCases = []struct {
	name string
	main string
}{
	// Immediately-invoked no-capture lambda with an argument.
	{"iife", `function main(): i32 { return (function(b: i32): i32 { return b + 1; })(4); }`},
	// Two-argument IIFE.
	{"iife-2arg", `function main(): i32 { return (function(a: i32, b: i32): i32 { return a + b; })(5, 6); }`},
	// No-capture lambda as a tuple element, called via `t.0(t.1)`.
	{"tuple-fn", `function main(): i32 { var t: ((i32) => i32, i32) = (function(x: i32): i32 { return x + 1; }, 10); return t.0(t.1); }`},
	// Assigning a no-capture lambda to a fn-typed local, then calling it.
	{"reassign", `function inc(b: i32): i32 { return b + 1; }
function main(): i32 { var f: (i32) => i32 = inc; f = function(x: i32): i32 { return x * 2; }; return f(5); }`},
	// Regression: a no-capture lambda call ARGUMENT still lowers (already lifted).
	{"arg-regress", `function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function main(): i32 { return apply(function(y: i32): i32 { return y + 1; }, 4); }`},
}

// TestSelfHostLambdaLiftPositionIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to the "ir" path.
func TestSelfHostLambdaLiftPositionIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range lambdaLiftPositionIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostLambdaLiftPositionIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostLambdaLiftPositionIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host lambda-lift-position wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range lambdaLiftPositionIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "lamlift_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("lambda-lift-position wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
