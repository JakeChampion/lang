package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostUnsignedCompareWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host unsigned-compare wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	runIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
		{"gt-helper-repro", `function u32_max(a: u32, b: u32): u32 { if (a > b) { return a; } return b; } function main(): i32 { let big: u32 = 4000000000 as u32; let one: u32 = 1 as u32; if (u32_max(big, one) == big) { return 42; } return 0; }`, 42},
		{"gt-direct", `function main(): i32 { let big: u32 = 4000000000 as u32; let one: u32 = 1 as u32; if (big > one) { return 42; } return 7; }`, 42},
		{"lt-direct", `function main(): i32 { let big: u32 = 3000000000 as u32; let one: u32 = 1 as u32; if (one < big) { return 5; } return 0; }`, 5},
		{"ge-direct", `function main(): i32 { let big: u32 = 2147483648 as u32; let one: u32 = 5 as u32; if (big >= one) { return 6; } return 0; }`, 6},
		{"le-direct", `function main(): i32 { let big: u32 = 4000000001 as u32; let one: u32 = 1 as u32; if (one <= big) { return 8; } return 0; }`, 8},
		{"both-high", `function main(): i32 { let a: u32 = 3000000000 as u32; let b: u32 = 4000000000 as u32; if (a < b) { return 9; } return 0; }`, 9},
		{"eq-still-works", `function main(): i32 { let big: u32 = 4000000000 as u32; if (big == (4000000000 as u32)) { return 3; } return 0; }`, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("unsigned-compare wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
