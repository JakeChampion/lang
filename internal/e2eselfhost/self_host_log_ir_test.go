package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostLogIRWasm pins `__log_f64(x)` (the lowering behind std/float's
// `(x: f64) log()`) on the wasm IR path. Like exp, ln had no wasm instruction or
// IR runtime (a wasm_eligible exclusion; the wasm AST path defers it too). flog
// now lowers to op_flog -> $__fern_log_f64, a fresh polynomial runtime: decompose
// x = m·2^e, normalize m to [√2/2,√2), f = (m−1)/(m+1), ln(m) = 2·(f + f³/3 + … +
// f¹¹/11) via a Horner polynomial in f², ln(x) = e·ln2 + ln(m) — the wasm sibling
// of asm_arm64's __fern_log_f64 (same coefficients). Self-contained f64 + i64
// bit math, no imports/heap.
//
// Value-tested (not differential — the wasm AST path has no log to diff against):
// the program computes ln(x) at a range of inputs (1, e, e², 0.5, 2, 1000) and
// checks each against the known f64 value within a 1e-6 ABSOLUTE tolerance
// (absolute, not relative, so ln(1)=0 is handled). Exits 0 only if every check
// passes; the test also pins that the IR path was taken (`call $__fern_log_f64`
// in the WAT).
func TestSelfHostLogIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host log wasm IR e2e")
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

	// `check` returns true when |log(x) - expected| <= 1e-6 (absolute).
	const src = `function check(x: f64, expected: f64): boolean {
    var got: f64 = __log_f64(x);
    return __abs_f64(got - expected) <= 0.000001;
}
function main(): i32 {
    if (!check(1.0, 0.0)) { return 1; }
    if (!check(2.718281828459045, 1.0)) { return 2; }
    if (!check(7.38905609893065, 2.0)) { return 3; }
    if (!check(0.5, -0.6931471805599453)) { return 4; }
    if (!check(2.0, 0.6931471805599453)) { return 5; }
    if (!check(1000.0, 6.907755278982137)) { return 6; }
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
	if !bytes.Contains(wat, []byte("call $__fern_log_f64")) {
		t.Fatal("log did not reach the wasm IR runtime path (no call $__fern_log_f64 in WAT)")
	}
	watFile := filepath.Join(dir, "log_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("log wasm IR program exited %d, want 0 (a check at input #%d exceeded 1e-6 absolute error)\n--- WAT ---\n%s", code, code, wat)
	}
}
