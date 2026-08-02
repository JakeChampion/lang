package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostTrigIRWasm pins `__sin_f64(x)` / `__cos_f64(x)` (the lowering behind
// std/float's `(x: f64) sin()` / `cos()`) on the wasm IR path — the last two libm
// transcendentals. They had no wasm instruction or IR runtime (wasm_eligible
// exclusions; the wasm AST path defers them too). fsin/fcos now lower to op_fsin/
// op_fcos -> $__fern_sin_f64 / $__fern_cos_f64, fresh polynomial runtimes:
// range-reduce k = round(x/(π/2)), r = x − k·(π/2) ∈ [−π/4, π/4], quadrant q = k&3
// selects ±sin(r)/±cos(r) from Taylor polynomials in r² — the wasm siblings of
// asm_arm64's __fern_sin_f64 / __fern_cos_f64 (same coefficients + quadrant logic).
// Self-contained f64 + i64 math, no imports/heap.
//
// Value-tested (not differential — the wasm AST path has no sin/cos to diff
// against): the program checks sin and cos at 0, π/6, π/4, π/2, π, 3π/2, 2π and a
// negative angle against the known f64 values within a 1e-5 ABSOLUTE tolerance
// (absolute since the outputs cross 0). Exits 0 only if every check passes; the
// test also pins that the IR path was taken (`call $__fern_sin_f64` +
// `call $__fern_cos_f64` in the WAT).
func TestSelfHostTrigIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host trig wasm IR e2e")
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

	// csin/ccos return true when |fn(x) - expected| is within tolerance.
	// 1e-5 absolute tolerance: the degree-6 cos / degree-7 sin Taylor polynomials
	// carry up to ~3.6e-6 truncation error at the range edge r = ±π/4 (the arm64
	// helper uses the same polynomials), so 1e-6 would be inside that noise.
	const src = `function csin(x: f64, expected: f64): boolean {
    return __abs_f64(__sin_f64(x) - expected) <= 0.00001;
}
function ccos(x: f64, expected: f64): boolean {
    return __abs_f64(__cos_f64(x) - expected) <= 0.00001;
}
function main(): i32 {
    if (!csin(0.0, 0.0)) { return 1; }
    if (!csin(0.5235987755982988, 0.5)) { return 2; }
    if (!csin(0.7853981633974483, 0.7071067811865476)) { return 3; }
    if (!csin(1.5707963267948966, 1.0)) { return 4; }
    if (!csin(3.141592653589793, 0.0)) { return 5; }
    if (!csin(4.71238898038469, -1.0)) { return 6; }
    if (!csin(6.283185307179586, 0.0)) { return 7; }
    if (!csin(-1.5707963267948966, -1.0)) { return 8; }
    if (!ccos(0.0, 1.0)) { return 9; }
    if (!ccos(1.0471975511965976, 0.5)) { return 10; }
    if (!ccos(0.7853981633974483, 0.7071067811865476)) { return 11; }
    if (!ccos(1.5707963267948966, 0.0)) { return 12; }
    if (!ccos(3.141592653589793, -1.0)) { return 13; }
    if (!ccos(6.283185307179586, 1.0)) { return 14; }
    if (!ccos(-3.141592653589793, -1.0)) { return 15; }
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
	if !bytes.Contains(wat, []byte("call $__fern_sin_f64")) || !bytes.Contains(wat, []byte("call $__fern_cos_f64")) {
		t.Fatal("trig did not reach the wasm IR runtime path (missing call $__fern_sin_f64 / $__fern_cos_f64 in WAT)")
	}
	watFile := filepath.Join(dir, "trig_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("trig wasm IR program exited %d, want 0 (a check at case #%d exceeded 1e-5 absolute error)\n--- WAT ---\n%s", code, code, wat)
	}
}
