package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostPowIRWasm pins `__pow_f64(x, y)` (the lowering behind std/float's
// `(x: f64) pow(y)`) on the wasm IR path. pow is the one BINARY transcendental
// and the last of the exp/log family: it was a wasm_eligible exclusion, and now
// that exp + log lower on wasm it does too. fpow lowers to op_fpow (an op_fbin) ->
// $__fern_pow_f64, a one-line runtime x^y = exp(y·ln x) composing the two
// polynomial helpers — the wasm sibling of asm_arm64's __fern_pow_f64. The two
// f64 operands arrive in stack order x then y (irlower's op_fbin), matching the
// param order.
//
// Value-tested (not differential — the wasm AST path has no pow to diff against):
// the program computes x^y at a range of inputs (2^10, 2^0.5, 9^0.5, 5^0, 10^-2,
// e^1) and checks each against the known f64 value within a 1e-6 RELATIVE
// tolerance. Exits 0 only if every check passes; the test also pins that the IR
// path was taken (`call $__fern_pow_f64` in the WAT).
func TestSelfHostPowIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host pow wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
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

	// `check` returns true when |pow(x,y) - expected| <= 1e-6 * |expected|.
	const src = `function check(x: f64, y: f64, expected: f64): boolean {
    var got: f64 = __pow_f64(x, y);
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
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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
