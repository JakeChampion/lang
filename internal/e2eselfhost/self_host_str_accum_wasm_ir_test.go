package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStrAccumWasmIR proves the string-builder accumulator reclaim (#2649)
// lowers correctly on the self-hosted WASM IR backend and — crucially — never
// DOUBLE-FREES the superseded growth-chain boxes. A wasm heap string is a single
// rc-headered block, so __fern_str_free maps to $__fern_arr_dec, whose over-release
// detector ($__fern_rc_underflow) ticks on any dec below rc 0. Each case runs the
// accumulator in a churn helper (lowered through the IR reclaim) and checks
// __rc_underflow_count() == 0 in main: a double-free surfaces as exit 99. The
// reset uses a loop-invariant operand + a fresh .to_ascii_upper() (both stay on the IR
// path under the underflow probe, unlike i32_to_string). Exit codes stay < 126.
func TestSelfHostStrAccumWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host string-accumulator wasm IR e2e")
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

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// Bounded accumulator (grow via `s + x`, reset to a fresh `x.to_ascii_upper()` at
		// len > 40) over 3000 iters. Every superseded box is freed exactly once; a
		// double-free would tick the underflow detector → 99. return 0.
		{"accum-churn", `function churn(n: i32): i32 { var x: string = "yy"; var s: string = ""; var i: i32 = 0; while (i < n) { s = s + x; if (s.len() > 40) { s = x.to_ascii_upper(); } i = i + 1; } return 0; } function main(): i32 { var r: i32 = churn(3000); if (__rc_underflow_count() != 0) { return 99; } return r; }`, 0},
		// Small accumulator whose final value's length is returned — exercises the
		// exit-sweep free of the final box with no double-free. "a"+"b"+"b"+"b" len 4.
		{"accum-len", `function build(): i32 { var s: string = "a"; var i: i32 = 0; while (i < 3) { s = s + "b"; i = i + 1; } return s.len(); } function main(): i32 { var r: i32 = build(); if (__rc_underflow_count() != 0) { return 99; } return r; }`, 4},
		// MOVE-ON-RETURN: a returned string builder called many times. The
		// intermediates are freed inside build (consume-rebind) and the final is
		// MOVED OUT (kept from build's exit sweep). A double-free of any superseded
		// or moved box would tick the underflow detector → 99. 500 builds, return 0.
		{"accum-return-churn", `function build(n: i32): string { var x: string = "z"; var s: string = ""; var i: i32 = 0; while (i < n) { s = s + x; i = i + 1; } return s; } function main(): i32 { var t: i32 = 0; var k: i32 = 0; while (k < 500) { var r: string = build(30); t = (t + r.len()) % 7; k = k + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
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
			if !strings.Contains(string(wat), "$__fern_str_box") {
				t.Errorf("%q did not reach the IR box path (no box in WAT)", tc.name)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("string accumulator wasm IR %q = %d, want %d (99 = double-free detected)", tc.name, got, tc.expected)
			}
		})
	}
}
