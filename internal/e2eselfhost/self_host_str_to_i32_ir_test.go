package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// strToI32IRCases exercise the builtin `str_to_i32(s)` lowering through the
// stack-IR path: the string box is parsed to an i32 via the __fern_str_to_i32
// runtime (optional leading '-', decimal digits, stops at the first non-digit;
// empty box -> 0). It is allocation-free (parses in registers / wasm locals),
// so no heap dependency. The exit code pins BOTH that the helper runs AND its
// parse result. The self-host compiler's own source uses this free form.
//
// The x86 / arm64 runtime bodies are transcribed from asm.fern / asm_arm64.fern's
// proven register-ABI __fern_str_to_i32 (stack-ABI wrappers); the wasm body is
// authored fresh (the wasm AST backend had no str_to_i32), so these hardcoded
// expectations are the oracle. Each value is <= 126 (cf. wasmtime exit-code gap).
var strToI32IRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Plain decimal.
	{"basic", `function main(): i32 { return str_to_i32("42"); }`, 42},
	// Zero.
	{"zero", `function main(): i32 { return str_to_i32("0"); }`, 0},
	// Leading '-' -> negative (re-negated so the exit code is in range).
	{"negative", `function main(): i32 { var n: i32 = str_to_i32("-5"); if (n < 0) { return 0 - n; } return n; }`, 5},
	// Stops at the first non-digit (trailing junk ignored).
	{"trailing-junk", `function main(): i32 { return str_to_i32("12x9"); }`, 12},
	// Empty box -> 0 (offset by +7 so a stray non-zero would show).
	{"empty", `function main(): i32 { return str_to_i32("") + 7; }`, 7},
	// Multi-digit accumulation.
	{"multidigit", `function main(): i32 { return str_to_i32("100"); }`, 100},
	// Round-trips with i32_to_string (the inverse conversion).
	{"roundtrip", `function main(): i32 { return str_to_i32(i32_to_string(123)); }`, 123},
	// Bound to a local, then used (the binding tracks an i32, not a string).
	{"bind-and-add", `function main(): i32 { var a: i32 = str_to_i32("40"); return a + 2; }`, 42},
}

// TestSelfHostStrToI32IRX86_64 compiles each case through the self-hosted x86-64
// driver (asm_run, IR default-on) and asserts the exit code.
func TestSelfHostStrToI32IRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range strToI32IRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostStrToI32IRWasm runs the same cases through the wasm IR backend
// (the wasm $__fern_str_to_i32 body is authored fresh — no AST oracle).
func TestSelfHostStrToI32IRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host str_to_i32 wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range strToI32IRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, "s2i_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("str_to_i32 wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
