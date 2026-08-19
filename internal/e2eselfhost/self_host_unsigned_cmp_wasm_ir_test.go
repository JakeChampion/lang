package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostUnsignedCompareWasmIR is the correctness gate for UNSIGNED
// (u32) ordering comparisons on the wasm IR backend (#2917). A u32 value
// >= 2^31 is a signed-negative i32, so a signed wasm compare (i32.lt_s /
// gt_s / le_s / ge_s) gives the wrong answer for it. irlower now flags an
// ordering-compare op `unsigned` when an operand is u32, and wasm_ir emits
// the i32.*_u opcode. (x86-64 / arm64 keep u32 positive in their 64-bit
// slots, so a signed compare already matched there — this brings the wasm
// IR path to parity.) Every value here is in the signed-negative u32 range,
// so each case fails with a signed compare and passes with an unsigned one.
// Expected values are the interpreter-oracle answers.
func TestSelfHostUnsignedCompareWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host unsigned-compare wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
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
		watFile := filepath.Join(dir, "ir_prog.wat")
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

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// The exact repro from #2917: u32_max(big, one) == big. big = 4e9
		// (> 2^31). With a signed compare big > one is false (big is negative
		// as i32) so the max is `one` and the test returns 0; unsigned -> 42.
		{"gt-helper-repro", `function u32_max(a: u32, b: u32): u32 { if (a > b) { return a; } return b; } function main(): i32 { var big: u32 = 4000000000 as u32; var one: u32 = 1 as u32; if (u32_max(big, one) == big) { return 42; } return 0; }`, 42},
		// `>` directly in main, no helper.
		{"gt-direct", `function main(): i32 { var big: u32 = 4000000000 as u32; var one: u32 = 1 as u32; if (big > one) { return 42; } return 7; }`, 42},
		// `<`: one < big is true unsigned, false signed (big negative).
		{"lt-direct", `function main(): i32 { var big: u32 = 3000000000 as u32; var one: u32 = 1 as u32; if (one < big) { return 5; } return 0; }`, 5},
		// `>=` with a big LHS and small RHS.
		{"ge-direct", `function main(): i32 { var big: u32 = 2147483648 as u32; var one: u32 = 5 as u32; if (big >= one) { return 6; } return 0; }`, 6},
		// `<=`: small <= big.
		{"le-direct", `function main(): i32 { var big: u32 = 4000000001 as u32; var one: u32 = 1 as u32; if (one <= big) { return 8; } return 0; }`, 8},
		// Two values both in the signed-negative range: 3e9 < 4e9 unsigned,
		// but as signed i32 3e9 (-1294967296) > 4e9 (-294967296) is false, so a
		// signed `<` would wrongly say 3e9 < 4e9 is false.
		{"both-high", `function main(): i32 { var a: u32 = 3000000000 as u32; var b: u32 = 4000000000 as u32; if (a < b) { return 9; } return 0; }`, 9},
		// eq/ne are sign-agnostic and must keep working alongside the change.
		{"eq-still-works", `function main(): i32 { var big: u32 = 4000000000 as u32; if (big == (4000000000 as u32)) { return 3; } return 0; }`, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("unsigned-compare wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
