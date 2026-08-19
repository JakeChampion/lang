package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// arrowLambdaIRCases pin the self-host IR lowering of the arrow-lambda spelling
// `(params): ret => expr` — specifically the CAPTURING-closure shapes through the
// self-hosted compiler's IR path. The self-host parser desugars an arrow lambda
// (arrow_lambda_at lookahead → parse_arrow_lambda, see parser.fern / #2701) to the
// SAME ExprLambda the verbose `function(params): ret { body }` form produces (an
// expression body becomes `[return expr]`), so the existing lambda-lift +
// closure-box machinery (lift_lambdas / closure_lift_one) lowers it unchanged.
//
// This complements internal/e2e/arrow_lambda_test.go, which exercises arrow lambdas
// only through the NATIVE Go backends with NON-capturing lambdas; here every case
// is routing-pinned to "ir" (so a regression off the IR path fails loudly) and
// the capturing cases cover the closure-lift path the native test never touches.
// Each case is oracle-checked against the interpreter and returns a value <= 120
// (cf. the wasmtime exit-code gap #2908).
var arrowLambdaIRCases = []struct {
	name string
	main string
}{
	// Capture-free arrow lambda, bound and called.
	{"noncap", `function main(): i32 { var f = (x: i32): i32 => x + 1; return f(5); }`},
	// Capturing an outer scalar.
	{"capture", `function main(): i32 { var n = 10; var f = (x: i32): i32 => x + n; return f(5); }`},
	// Zero-arg capturing arrow lambda.
	{"capture-noargs", `function main(): i32 { var n = 7; var f = (): i32 => n * 2; return f(); }`},
	// Two params + a capture.
	{"two-params-cap", `function main(): i32 { var k = 3; var f = (a: i32, b: i32): i32 => a + b + k; return f(4, 5); }`},
	// Capture used twice in the body expression.
	{"capture-twice", `function main(): i32 { var n = 6; var f = (x: i32): i32 => x * n + n; return f(4); }`},
	// Regression: the function(){} closure form still lowers.
	{"fn-form-regress", `function main(): i32 { var n = 10; var f = function(x: i32): i32 { return x + n; }; return f(5); }`},
}

// TestSelfHostArrowLambdaIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, routing pinned to "ir".
func TestSelfHostArrowLambdaIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range arrowLambdaIRCases {
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

// TestSelfHostArrowLambdaIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostArrowLambdaIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arrow-lambda wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range arrowLambdaIRCases {
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
			watFile := filepath.Join(dir, "arrow_lambda_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("arrow-lambda wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
