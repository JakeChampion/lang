package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// i64CallWidthIRCases pin an i32-returning CALL consumed in an i64 arithmetic
// context (`s64 + g()`) to the self-host IR path on x86-64 + wasm. lower_i64's
// ExprCall arm lowered only i64-returning calls (and the 0-arg if/match IIFE)
// and bailed everything else via `return s.fail()`, dropping the whole module to
// the legacy AST emitter. #2691 widens it: a width-32 call result (not
// i64-returning, not the IIFE) is lowered via lower_expr (the normal call path)
// and sign-extended to i64 (op_int_extend). This is provably safe — the checker
// forbids i64 + f64/string/u32 and rejects binding a bare i32 call to an i64
// (E009; it needs an explicit `as i64`), so a call reaching this point in a valid
// program necessarily returns a signed i32. This is the last of the four i32-leaf
// shapes feeding lower_i64 (after the i32 ident, array element, and struct/tuple
// member widenings). Each case narrows the i64 result with `as i32` (valid wasm
// exit code in [0,126)) and is oracle-checked against the interpreter.
var i64CallWidthIRCases = []struct {
	name string
	main string
}{
	// i64 local + i32-returning free function. 30 + 12 = 42.
	{"call-free", `function g(): i32 { return 12; } function main(): i32 { var s: i64 = 30; return (s + g()) as i32; }`},
	// Call with an argument. 30 + (6*2) = 42.
	{"call-arg", `function g(x: i32): i32 { return x * 2; } function main(): i32 { var s: i64 = 30; return (s + g(6)) as i32; }`},
	// Sign-extension: a call returning a NEGATIVE i32 must sign-extend. 50 + (-8) = 42.
	{"call-neg", `function g(): i32 { return -8; } function main(): i32 { var s: i64 = 50; return (s + g()) as i32; }`},
	// i32-returning METHOD call. 30 + 12 = 42.
	{"call-method", `struct C { n: i32 } function (c: C) val(): i32 { return c.n; } function main(): i32 { var c: C = C { n: 12 }; var s: i64 = 30; return (s + c.val()) as i32; }`},
	// Call inside a for-range accumulating into i64. inc(0)+inc(1)+inc(2) = 1+2+3 = 6.
	{"call-loop", `function inc(x: i32): i32 { return x + 1; } function main(): i32 { var s: i64 = 0; for i in 0..3 { s = s + inc(i); } return s as i32; }`},
	// Regression: an i64-returning call still lowers as a native i64. 0 + 42 = 42.
	{"call-i64-keep", `function g(): i64 { return 42; } function main(): i32 { var s: i64 = 0; return (s + g()) as i32; }`},
}

// TestSelfHostI64CallWidthIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostI64CallWidthIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range i64CallWidthIRCases {
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

// TestSelfHostI64CallWidthIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostI64CallWidthIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host i64-call-width wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range i64CallWidthIRCases {
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
			watFile := filepath.Join(dir, "i64_call_width_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("i64-call-width wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
