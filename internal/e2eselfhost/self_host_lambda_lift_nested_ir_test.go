package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// lambdaLiftNestedIRCases exercise no-capture lambda CALLS nested inside compound
// expressions — binary / unary / index — which the lambda-lift pre-pass now
// reaches by recursing through those forms in lift_expr_walk. Descending only
// into call-arg / array / struct-field / tuple / callee positions leaves a
// lambda call inside `(...) + 1`, `0 - (...)`, or `a[...]`
// survived unlifted and the module bailed to the AST path.
//
// All lambdas here are no-capture (lifted to a top-level `__lam_N`); a capturing
// lambda nested the same way still bails (needs the env-passing closure form).
// Each case is oracle-checked against the interpreter, routing-pinned to "ir",
// and returns a non-negative value <= 126 (avoiding the wasmtime exit-code
// truncation gap and the negative-exit-code ambiguity, cf. #2908).
var lambdaLiftNestedIRCases = []struct {
	name string
	main string
}{
	// IIFE in the left operand of a binary op: (5*3) + 1 = 16.
	{"iife-binary-lhs", `function main(): i32 { return (function(x: i32): i32 { return x * 3; })(5) + 1; }`},
	// IIFE in the right operand: 1 + (5*3) = 16.
	{"iife-binary-rhs", `function main(): i32 { return 1 + (function(x: i32): i32 { return x * 3; })(5); }`},
	// IIFE under a unary minus, kept positive: 100 - (5+1) = 94.
	{"iife-unary", `function main(): i32 { return 100 - (function(x: i32): i32 { return x + 1; })(5); }`},
	// IIFE as an array index: a[(1+1)] = a[2] = 30.
	{"iife-index", `function main(): i32 { var a: i32[] = [10, 20, 30]; return a[(function(x: i32): i32 { return x + 1; })(1)]; }`},
	// Lambda call ARGUMENT inside a binary op: ap(\x.x+1)=4, +1 = 5.
	{"lambda-arg-binary", `function ap(f: (i32) => i32): i32 { return f(3); }
function main(): i32 { return ap(function(x: i32): i32 { return x + 1; }) + 1; }`},
	// Nested deeper: a binary whose operands are both IIFE calls: 6 + 8 = 14.
	{"iife-both-operands", `function main(): i32 { return (function(x: i32): i32 { return x + 1; })(5) + (function(y: i32): i32 { return y * 2; })(4); }`},
}

// TestSelfHostLambdaLiftNestedIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to the "ir" path.
func TestSelfHostLambdaLiftNestedIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range lambdaLiftNestedIRCases {
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

// TestSelfHostLambdaLiftNestedIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostLambdaLiftNestedIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host lambda-lift-nested wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range lambdaLiftNestedIRCases {
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
			watFile := filepath.Join(dir, "lamnest_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("lambda-lift-nested wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
