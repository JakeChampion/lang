package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// i64MixWidthIRCases pin an i32-width scalar leaf consumed in an i64 ARITHMETIC
// context (`s64 + i32`, a mixed-width local/param, a for-range loop var
// accumulated into an i64) to the self-host IR path on x86-64 + wasm. lower_i64
// must accept more than leaves that are ALREADY i64 (an i64 slot/literal/cast
// or an i64-returning call): a plain i32 identifier hitting `return s.fail()`
// bails the whole module to the legacy AST emitter. #2691 widens lower_i64's
// ExprIdent arm: an i32-scalar slot (is_i32_scalar_slot) is lowered as an i32 and
// sign/zero-extended to i64 (op_int_extend; zero-extend for u32, sign-extend for
// signed i32 / subword). Because the ExprBinary arm recurses through lower_i64,
// this also lifts the i32 leaves of nested sub-expressions for free. Each case
// narrows the i64 result with `as i32` so the wasm `_start` exit code is a valid
// i32 in [0,126); each is oracle-checked against the interpreter.
var i64MixWidthIRCases = []struct {
	name string
	main string
}{
	// for-range loop var (i32) accumulated into an i64. 0+1+2+3+4 = 10.
	{"range-acc", `function main(): i32 { var s: i64 = 0; for i in 0..5 { s = s + i; } return s as i32; }`},
	// Inclusive range. 1+2+3+4+5 = 15.
	{"range-incl", `function main(): i32 { var s: i64 = 0; for i in 1..=5 { s = s + i; } return s as i32; }`},
	// i64 local + i32 local. 40 + 2 = 42.
	{"mix-local", `function main(): i32 { var i: i32 = 2; var s: i64 = 40; return (s + i) as i32; }`},
	// i64 local + i32 PARAM. 39 + 3 = 42.
	{"mix-param", `function f(a: i32): i64 { var s: i64 = 39; return s + a; } function main(): i32 { return f(3) as i32; }`},
	// i32 leaf inside a nested sub-expression (`s + (i + 2)`). 30 + (5+2) = 37.
	{"mix-nested", `function main(): i32 { var i: i32 = 5; var s: i64 = 30; return (s + (i + 2)) as i32; }`},
	// i64 * i32. 14 * 3 = 42.
	{"mix-mul", `function main(): i32 { var i: i32 = 3; var s: i64 = 14; return (s * i) as i32; }`},
	// Sign-extension: a NEGATIVE i32 must sign-extend (not zero-extend). 50 + (-8) = 42.
	{"neg-sign", `function main(): i32 { var i: i32 = -8; var s: i64 = 50; return (s + i) as i32; }`},
	// Accumulate over 100 iterations in the i64 domain, then narrow. sum(0..99)=4950, /100 = 49.
	{"big-acc", `function main(): i32 { var s: i64 = 0; for i in 0..100 { s = s + i; } return (s / 100) as i32; }`},
}

// TestSelfHostI64MixWidthIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostI64MixWidthIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range i64MixWidthIRCases {
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

// TestSelfHostI64MixWidthIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostI64MixWidthIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host i64-mix-width wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range i64MixWidthIRCases {
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
			watFile := filepath.Join(dir, "i64_mix_width_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("i64-mix-width wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
