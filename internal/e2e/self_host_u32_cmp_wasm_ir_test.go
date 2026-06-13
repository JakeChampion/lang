package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostU32CmpWasmIR proves the self-hosted wasm IR backend lowers u32
// comparisons as UNSIGNED (#2917). u32 is a native 32-bit i32 on wasm, so a
// signed `i32.lt_s`/`gt_s`/… is wrong once bit 31 is set (a value above 2^31
// reads as negative): `4_000_000_000 > 1` came out FALSE. irlower now selects
// the `i32.{lt,le,gt,ge}_u` opcodes for u32 operands (the u32 analog of the u64
// unsigned-compare work in #2904; the x86/arm64 register backends were already
// correct because u32 values are zero-extended in 64-bit registers).
//
// Expected values are the unsigned-correct answers (interp-verified); each case
// is bit-31-sensitive so it differs from the signed op. Exit codes <= 125
// (WASI proc_exit).
func TestSelfHostU32CmpWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host u32 compare wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// big = 4e9 (> 2^31, signed-negative as i32). Unsigned: big > one TRUE.
		{"gt-u", `function main(): i32 { var big: u32 = 4000000000 as u32; var one: u32 = 1 as u32; if (big > one) { return 42; } return 0; }`, 42},
		// one <= big TRUE unsigned (signed: 1 <= negative is FALSE).
		{"le-u", `function main(): i32 { var big: u32 = 4000000000 as u32; var one: u32 = 1 as u32; if (one <= big) { return 7; } return 0; }`, 7},
		// both above 2^31: 4.0e9 < 4.1e9 TRUE unsigned.
		{"lt-u-both-high", `function main(): i32 { var a: u32 = 4000000000 as u32; var b: u32 = 4100000000 as u32; if (a < b) { return 5; } return 0; }`, 5},
		// >= unsigned on a high value vs itself.
		{"ge-u", `function main(): i32 { var a: u32 = 4000000000 as u32; if (a >= (4000000000 as u32)) { return 9; } return 0; }`, 9},
		// A u32 max via param + return, then compare the result (param/return path).
		{"param-ret", `function umax(a: u32, b: u32): u32 { if (a > b) { return a; } return b; } function main(): i32 { var big: u32 = 4000000000 as u32; var one: u32 = 1 as u32; if (umax(big, one) == big) { return 11; } return 0; }`, 11},
	}
	for _, tc := range cases {
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
			watFile := filepath.Join(dir, "ir_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("u32 compare wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
