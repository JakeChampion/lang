package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostPowIRWasm pins `__pow_f64(x, y)` (the lowering behind std/float's
// `(x: f64) pow(y)`) on the wasm IR path. pow is the one BINARY transcendental:
// fpow lowers to $__fern_pow_f64, a one-line runtime x^y = exp(y·ln x)
// composing the exp and log polynomial helpers — the wasm sibling of
// asm_arm64's __fern_pow_f64. The two f64 operands arrive in stack order x then
// y, matching the param order.
//
// Value-tested: the program computes x^y at a range of inputs (2^10, 2^0.5,
// 9^0.5, 5^0, 10^-2, e^1) and checks each against the known f64 value within a
// 1e-6 RELATIVE tolerance. Exits 0 only if every check passes; the test also
// pins that the IR path was taken (`call $__fern_pow_f64` in the WAT).
func TestSelfHostPowIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host pow wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	// `check` returns true when |pow(x,y) - expected| <= 1e-6 * |expected|.
	const src = `function check(x: f64, y: f64, expected: f64): boolean {
    let got: f64 = __pow_f64(x, y);
    return __abs_f64(got - expected) <= (__abs_f64(expected) * 0.000001);
}
function main(): i32 {
    if (!check(2.0, 10.0, 1024.0)) { return 1; }
    if (!check(2.0, 0.5, 1.4142135623730951)) { return 2; }
    if (!check(9.0, 0.5, 3.0)) { return 3; }
    if (!check(5.0, 0.0, 1.0)) { return 4; }
    if (!check(10.0, -2.0, 0.01)) { return 5; }
    if (!check(2.718281828459045, 1.0, 2.718281828459045)) { return 6; }
    return 0;
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("call $__fern_pow_f64")) {
		t.Fatal("pow did not reach the wasm IR runtime path (no call $__fern_pow_f64 in WAT)")
	}
	watFile := filepath.Join(dir, "pow_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("pow wasm IR program exited %d, want 0 (a check at input #%d exceeded 1e-6 relative error)\n--- WAT ---\n%s", code, code, wat)
	}
}
