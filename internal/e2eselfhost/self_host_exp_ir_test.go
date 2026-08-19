package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostExpIRWasm pins `__exp_f64(x)` (the lowering behind std/float's
// `(x: f64) exp()`) on the wasm IR path. The libm transcendentals had no wasm
// instruction or IR runtime, so a module computing e^x was a wasm_eligible
// exclusion (the wasm AST path defers them too — they simply didn't work on wasm).
// fexp now lowers to op_fexp -> $__fern_exp_f64, a fresh polynomial runtime: e^x =
// 2^k · Taylor7(r), k = round(x·log2e), r = x − k·ln2, with 2^k built directly in
// the f64 exponent bits — the wasm sibling of asm_arm64's __fern_exp_f64 (same
// coefficients). Self-contained f64 math, no imports/heap.
//
// Value-tested (not differential — the wasm AST path has no exp to diff against):
// the program computes e^x at a range of inputs (0, ±1, 0.5, 2, 10) and checks
// each against the known f64 value within a 1e-6 RELATIVE tolerance, comfortably
// inside the ~7e-9 worst-case error of the degree-7 polynomial. Exits 0 only if
// every check passes; the test also pins that the IR path was taken
// (`call $__fern_exp_f64` in the WAT).
func TestSelfHostExpIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host exp wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	// `check` returns true when |exp(x) - expected| <= 1e-6 * |expected|.
	const src = `function check(x: f64, expected: f64): boolean {
    var got: f64 = __exp_f64(x);
    var err: f64 = __abs_f64(got - expected);
    return err <= (__abs_f64(expected) * 0.000001);
}
function main(): i32 {
    if (!check(0.0, 1.0)) { return 1; }
    if (!check(1.0, 2.718281828459045)) { return 2; }
    if (!check(2.0, 7.38905609893065)) { return 3; }
    if (!check(-1.0, 0.36787944117144233)) { return 4; }
    if (!check(0.5, 1.6487212707001282)) { return 5; }
    if (!check(10.0, 22026.465794806718)) { return 6; }
    return 0;
}`

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
	if !bytes.Contains(wat, []byte("call $__fern_exp_f64")) {
		t.Fatal("exp did not reach the wasm IR runtime path (no call $__fern_exp_f64 in WAT)")
	}
	watFile := filepath.Join(dir, "exp_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exp wasm IR program exited %d, want 0 (a check at input #%d exceeded 1e-6 relative error)\n--- WAT ---\n%s", code, code, wat)
	}
}
