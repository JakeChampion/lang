package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fnRetIRCases exercise functions that RETURN a function value through the
// self-host IR path on x86-64 + wasm.
//
// The gap this closes: a function returning a NO-CAPTURE lambda
// (`function mk(): (i32) => i32 { return function(b) { ... }; }`) bailed to the
// AST path, even though returning a *capturing* lambda and returning a *named*
// function both already lowered. `lift_lambdas` hoisted no-capture lambdas in
// call-argument / array-element / struct-field positions but not in RETURN
// position, so the bare lambda survived to lowering and tripped the bail. The fix
// lifts the return value via lift_call_arg, turning `return function(b){...}`
// into `return __lam_N` — the already-working named-function-return path.
//
// The capturing-return and named-return cases are included as regression guards
// (they must stay on the IR path and correct). Each case is oracle-checked
// against the interpreter, routing-pinned to "ir", and returns a value <= 126
// (wasmtime exit-code truncation, cf. #2908).
var fnRetIRCases = []struct {
	name string
	main string
}{
	// Return a no-capture lambda, bind it, then call it: 4 + 1 = 5.
	{"nocap", `function mk(): (i32) => i32 { return function(b: i32): i32 { return b + 1; }; }
function main(): i32 { var g = mk(); return g(4); }`},
	// No-capture lambda body with multiplication: 4 * 3 = 12.
	{"nocap-mul", `function mk(): (i32) => i32 { return function(b: i32): i32 { return b * 3; }; }
function main(): i32 { var g = mk(); return g(4); }`},
	// Bound result called twice: (4+1) + (10+1) = 16.
	{"nocap-twice", `function mk(): (i32) => i32 { return function(b: i32): i32 { return b + 1; }; }
function main(): i32 { var g = mk(); return g(4) + g(10); }`},
	// Two-parameter no-capture returned lambda: 5 + 6 = 11.
	{"nocap-2arg", `function mk(): (i32, i32) => i32 { return function(a: i32, b: i32): i32 { return a + b; }; }
function main(): i32 { var g = mk(); return g(5, 6); }`},
	// Regression: returning a CAPTURING lambda still lowers (4 + 10 = 14).
	{"cap-regress", `function mk(n: i32): (i32) => i32 { return function(b: i32): i32 { return b + n; }; }
function main(): i32 { var g = mk(10); return g(4); }`},
	// Regression: returning a NAMED function still lowers (4 + 1 = 5).
	{"named-regress", `function inc(b: i32): i32 { return b + 1; }
function mk(): (i32) => i32 { return inc; }
function main(): i32 { var g = mk(); return g(4); }`},
}

// TestSelfHostFnRetIRX86_64 routes each case through the self-hosted x86-64 IR
// driver, oracle-checked, with routing pinned to the "ir" path.
func TestSelfHostFnRetIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range fnRetIRCases {
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

// TestSelfHostFnRetIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostFnRetIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fn-return wasm IR e2e")
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

	for _, tc := range fnRetIRCases {
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
			watFile := filepath.Join(dir, "fnret_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("fn-return wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
