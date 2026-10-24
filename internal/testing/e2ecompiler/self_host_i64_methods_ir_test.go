package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostI64MethodsIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host i64-methods wasm IR e2e")
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
		{"ret-field", `struct C { base: i64 } function (c: C) get(): i64 { return c.base; } function main(): i32 { let c = C { base: 20000000000 }; let r: i64 = c.get(); if (r > 15000000000) { return 8; } return 0; }`, 8},
		{"param-ret", `struct C { base: i64 } function (c: C) add(x: i64): i64 { return c.base + x; } function main(): i32 { let c = C { base: 5000000000 }; let r: i64 = c.add(6000000000); if (r > 10000000000) { return 5; } return 0; }`, 5},
		{"param-i32ret", `struct C { base: i64 } function (c: C) over(x: i64): i32 { if (c.base + x > 12000000000) { return 1; } return 0; } function main(): i32 { let c = C { base: 7000000000 }; return c.over(6000000000); }`, 1},
		{"loop", `struct C { step: i64 } function (c: C) s(): i64 { return c.step; } function main(): i32 { let c = C { step: 3000000000 }; let acc: i64 = 0; let i = 0; while (i < 4) { acc = acc + c.s(); i = i + 1; } if (acc > 11000000000) { return 9; } return 0; }`, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("i64-method wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
