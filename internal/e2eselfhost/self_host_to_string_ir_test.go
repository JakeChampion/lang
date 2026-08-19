package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// toStringIRCases exercise the builtin `.to_string()` lowering through the
// stack-IR path: an i32 receiver routes to the __fern_i32_to_string runtime
// helper (decimal-text box), a string receiver is identity, and a struct's
// @derive(Display)-generated `to_string` composes leaf `.to_string()` calls via
// `+` concat. Each case returns an i32 derived from the produced string's bytes
// / length so the exit code pins BOTH that the helper runs AND its formatting.
var toStringIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// (42).to_string() == "42": len 2, bytes '4','2'.
	{"i32-basic",
		`function main(): i32 { var a: string = (42).to_string(); if (a.len() != 2) { return 50; } if (a[0] != 52) { return 51; } if (a[1] != 50) { return 52; } return a.len(); }`, 2},
	// 0 -> "0" (the zero special-case), negative -> leading '-'.
	{"i32-zero-and-negative",
		`function main(): i32 { var z: string = (0).to_string(); var n: string = (5 - 12).to_string(); if (z.len() != 1) { return 60; } if (z[0] != 48) { return 61; } if (n.len() != 2) { return 62; } if (n[0] != 45) { return 63; } if (n[1] != 55) { return 64; } return z.len() + n.len(); }`, 3},
	// string.to_string() is identity — same bytes, same length.
	{"string-identity",
		`function main(): i32 { var s: string = "hi"; var t: string = s.to_string(); if (t.len() != 2) { return 70; } if (t[0] != 104) { return 71; } if (t[1] != 105) { return 72; } return t.len(); }`, 2},
	// to_string() result feeds `+` concat (the Display shape): "n=" + (7).to_string() == "n=7".
	{"concat-with-to-string",
		`function main(): i32 { var msg: string = "n=" + (7).to_string(); if (msg.len() != 3) { return 80; } if (msg[0] != 110) { return 81; } if (msg[2] != 55) { return 82; } return msg.len(); }`, 3},
	// @derive(Display): the generated P.to_string composes leaf i32 .to_string()
	// calls. "P(1, 2)" is 7 bytes (the exact derive format may differ, so assert
	// only that a non-empty string is produced and starts with 'P').
	{"derive-display",
		`trait Display { function to_string(self: Self): string; } @derive(Display) struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 1, y: 2 }; var s: string = p.to_string(); if (s.len() < 1) { return 90; } if (s[0] != 80) { return 91; } return 1; }`, 1},
	// INT_MIN — the one i32 whose negation overflows back to itself (#6050).
	// The helper carried its magnitude SIGNED, so the wrapped value stayed
	// negative, the digit loop never ran, and -2147483648 rendered as a bare
	// "-": sign emitted, every digit dropped. The value arrives through a call
	// so it cannot be constant-folded into a literal string. Asserts the exact
	// length and the first / second / last byte, so a formatter that produces
	// the right LENGTH of wrong digits still fails.
	{"i32-int-min",
		`function inc(n: i32): i32 { return n + 1; } function main(): i32 { var s: string = inc(2147483647).to_string(); if (s.len() != 11) { return 90; } if (s[0] != 45) { return 91; } if (s[1] != 50) { return 92; } if (s[10] != 56) { return 93; } return 11; }`, 11},
	// multi-digit + boundary: (100).to_string() == "100", (9).to_string() == "9".
	{"i32-multidigit",
		`function main(): i32 { var a: string = (100).to_string(); var b: string = (9).to_string(); if (a.len() != 3) { return 40; } if (a[0] != 49) { return 41; } if (a[1] != 48) { return 42; } if (b.len() != 1) { return 43; } return a.len() + b.len(); }`, 4},
	// Free-function spelling `i32_to_string(n)` — the same __fern_i32_to_string
	// op as `(n).to_string()`, just the free-call form the self-host source uses.
	// Same byte/length assertions as i32-basic, but via the free call.
	{"free-i32-basic",
		`function main(): i32 { var a: string = i32_to_string(42); if (a.len() != 2) { return 50; } if (a[0] != 52) { return 51; } if (a[1] != 50) { return 52; } return a.len(); }`, 2},
	// Free call: 0 -> "0", negative -> leading '-' (matches i32-zero-and-negative).
	{"free-i32-zero-and-negative",
		`function main(): i32 { var z: string = i32_to_string(0); var n: string = i32_to_string(5 - 12); if (z.len() != 1) { return 60; } if (z[0] != 48) { return 61; } if (n.len() != 2) { return 62; } if (n[0] != 45) { return 63; } if (n[1] != 55) { return 64; } return z.len() + n.len(); }`, 3},
	// Free call feeds `+` concat: "n=" + i32_to_string(7) == "n=7".
	{"free-concat",
		`function main(): i32 { var msg: string = "n=" + i32_to_string(7); if (msg.len() != 3) { return 80; } if (msg[0] != 110) { return 81; } if (msg[2] != 55) { return 82; } return msg.len(); }`, 3},
	// chr(n): an i32 byte to a fresh 1-char string box (the inverse of `s[0]`).
	// len is 1, byte 0 is n; the result feeds `.len()` / `[i]` / `+` concat as a
	// string. chr(65) == "A".
	{"chr-basic",
		`function main(): i32 { var a: string = chr(65); if (a.len() != 1) { return 30; } if (a[0] != 65) { return 31; } return a.len(); }`, 1},
	// chr result feeds `+` concat: chr(72) + chr(105) == "Hi" (len 2, bytes 72,105).
	{"chr-concat",
		`function main(): i32 { var s: string = chr(72) + chr(105); if (s.len() != 2) { return 40; } if (s[0] != 72) { return 41; } if (s[1] != 105) { return 42; } return s.len(); }`, 2},
}

// TestSelfHostToStringIRX86_64 compiles each case through the self-hosted x86-64
// driver (asm_run, IR default-on) and asserts the exit code.
func TestSelfHostToStringIRX86_64(t *testing.T) {
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

	for _, tc := range toStringIRCases {
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

// TestSelfHostToStringIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostToStringIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host to_string wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range toStringIRCases {
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
			watFile := filepath.Join(dir, "ts_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("to_string wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
