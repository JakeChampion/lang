package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// f32 shares the 8-byte f64 slot on the self-host IR path (#4366), so f32
// arithmetic (fadd/fsub/fmul/fdiv) computes at DOUBLE precision. Native gives
// f32 a true 4-byte slot and rounds the result after every op, so a value that
// is not f32-representable diverged. irlower now rounds the result of an f32
// arithmetic op via the f32_bits/f32_from_bits round-trip (the arithmetic
// sibling of the `as f32` cast rounding); a float COMPARISON (is_fcmp_kind)
// yields an i32 bool and is left untouched, and f64 arithmetic keeps full
// precision.
//
// Pinned to the native oracle (verified separately, interp + compiled). The
// canonical case: 16777216.0f + 1.0f = 2^24 + 1, which is not representable in
// f32 and rounds back to 2^24 — so `(a + one) as f64 == 16777216.0` holds under
// true f32 (returns 1) but not under f64 (which keeps 16777217.0, returns 0).
var f32ArithCases = []struct {
	name     string
	src      string
	expected int
}{
	// f32 add rounds: 2^24 + 1 -> 2^24 -> 1
	{"add-round", `function main(): i32 { var a: f32 = 16777216.0 as f32; var one: f32 = 1.0 as f32; var b: f32 = a + one; if ((b as f64) == 16777216.0) { return 1; } return 0; }`, 1},
	// exact f32 add: 1.5 + 2.5 = 4.0 -> 4
	{"add-exact", `function main(): i32 { var a: f32 = 1.5 as f32; var b: f32 = 2.5 as f32; var c: f32 = a + b; return c as i32; }`, 4},
	// f32 sub is exact for these: 8.5 - 2.0 = 6.5 -> 6
	{"sub-exact", `function main(): i32 { var a: f32 = 8.5 as f32; var b: f32 = 2.0 as f32; var c: f32 = a - b; return c as i32; }`, 6},
	// f32 mul is exact: 2.5 * 3.0 = 7.5 -> 7
	{"mul-exact", `function main(): i32 { var a: f32 = 2.5 as f32; var b: f32 = 3.0 as f32; var c: f32 = a * b; return c as i32; }`, 7},
	// REGRESSION: f64 arithmetic must NOT round — 2^24 + 1 stays 2^24+1 -> 1
	{"f64-noround", `function main(): i32 { var a: f64 = 16777216.0; var one: f64 = 1.0; var b: f64 = a + one; if (b == 16777217.0) { return 1; } return 0; }`, 1},
	// REGRESSION: an f32 comparison yields a bool and must not be rounded -> 7
	{"cmp", `function main(): i32 { var a: f32 = 2.5 as f32; var b: f32 = 3.5 as f32; if (a < b) { return 7; } return 0; }`, 7},
	// REGRESSION: an f32 arithmetic result feeding a comparison -> 8
	{"arith-then-cmp", `function main(): i32 { var a: f32 = 1.5 as f32; var b: f32 = 2.5 as f32; var c: f32 = a + b; if (c > (3.5 as f32)) { return 8; } return 0; }`, 8},
}

// TestSelfHostF32ArithWasmIR pins f32 arithmetic rounding on the wasm IR backend.
func TestSelfHostF32ArithWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host f32-arith wasm IR e2e")
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

	runIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		watFile := filepath.Join(dir, "f32a_prog.wat")
		if err := os.WriteFile(watFile, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		run := exec.Command("wasmtime", "run", watFile)
		_ = run.Run()
		if run.ProcessState == nil || !run.ProcessState.Exited() {
			t.Fatalf("wasmtime did not exit normally for %q:\n%s", src, wat)
		}
		return run.ProcessState.ExitCode()
	}

	for _, tc := range f32ArithCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("f32-arith wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}

// TestSelfHostF32ArithX86IR pins f32 arithmetic rounding on the x86-64 IR backend.
func TestSelfHostF32ArithX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	runIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		innerAsm := filepath.Join(dir, "f32a_inner.s")
		innerBin := filepath.Join(dir, "f32a_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc for %q: %v\n%s", src, err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally for %q", src)
		}
		return inner.ProcessState.ExitCode()
	}

	for _, tc := range f32ArithCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("f32-arith x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
