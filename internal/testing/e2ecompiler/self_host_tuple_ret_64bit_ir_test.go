package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostTupleRet64bitIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host tuple-ret-64bit wasm IR e2e")
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
		{"ret-i64-destructure", `function mk(): (i64, i32) { return (20000000000, 3); } function main(): i32 { let (a, b) = mk(); if (a > 15000000000) { return 7; } return b; }`, 7},
		{"ret-i64-dotaccess", `function mk(): (i64, i32) { return (9000000000, 4); } function main(): i32 { let t = mk(); if (t.0 > 8000000000) { return t.1; } return 0; }`, 4},
		{"ret-f64-destructure", `function mk(): (f64, i32) { return (2.5, 1); } function main(): i32 { let (x, n) = mk(); if (x > 2.0) { return 6; } return n; }`, 6},
		{"ret-i64-f64-mixed", `function mk(): (i64, f64) { return (6000000000, 3.5); } function main(): i32 { let (a, x) = mk(); let ok = 0; if (a > 5000000000) { ok = ok + 5; } if (x > 3.0) { ok = ok + 4; } return ok; }`, 9},
		{"ret-i64-arith", `function mk(): (i64, i64) { return (6000000000, 7000000000); } function main(): i32 { let (a, b) = mk(); let s: i64 = a + b; if (s > 12000000000) { return 8; } return 1; }`, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("tuple-ret-64bit wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
