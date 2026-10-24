package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostI64ArrayIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host i64-array wasm IR e2e")
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
		{"read", `function main(): i32 { let a: i64[] = [5000000000, 6000000000]; let s: i64 = a[0] + a[1]; if (s > 10000000000) { return 7; } return 0; }`, 7},
		{"read-small", `function main(): i32 { let a: i64[] = [1, 2, 3]; let s: i64 = a[0] + a[1]; return s as i32; }`, 3},
		{"loop", `function main(): i32 { let a: i64[] = [10000000000, 20000000000, 30000000000]; let s: i64 = 0; let i = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } if (s > 50000000000) { return 9; } return 0; }`, 9},
		{"param", `function sum(a: i64[]): i64 { return a[0] + a[1]; } function main(): i32 { let arr: i64[] = [5000000000, 6000000000]; let r: i64 = sum(arr); if (r > 10000000000) { return 5; } return 0; }`, 5},
		{"write", `function main(): i32 { let a: i64[] = [1000000000, 2000000000]; a = a.with(1, 9000000000); let s: i64 = a[0] + a[1]; if (s > 9900000000) { return 8; } return 0; }`, 8},
		{"write-loop", `function main(): i32 { let a: i64[] = [0, 0, 0]; let i = 0; while (i < 3) { a = a.with(i, (i as i64) * 10000000000); i = i + 1; } let s: i64 = a[0] + a[1] + a[2]; if (s > 25000000000) { return 6; } return 0; }`, 6},
		{"forin", `function main(): i32 { let a: i64[] = [10000000000, 20000000000, 30000000000]; let s: i64 = 0; for x in a { s = s + x; } if (s > 50000000000) { return 9; } return 0; }`, 9},
		{"forin-cmp", `function main(): i32 { let a: i64[] = [10000000000, 20000000000, 30000000000, 5000000000]; let c = 0; for x in a { if (x > 15000000000) { c = c + 1; } } return c; }`, 2},
		{"slice", `function main(): i32 { let a: i64[] = [10000000000, 20000000000, 30000000000, 40000000000]; let b = a[1:3]; let s: i64 = b[0] + b[1]; if (s > 40000000000) { return 7; } return 0; }`, 7},
		{"slice-len", `function main(): i32 { let a: i64[] = [10000000000, 20000000000, 30000000000, 40000000000]; let b = a[1:3]; return b.len(); }`, 2},
		{"slice-direct-index", `function main(): i32 { let a: i64[] = [10000000000, 20000000000, 30000000000, 40000000000]; if (a[1:3][1] > 25000000000) { return 6; } return 0; }`, 6},
		{"ret", `function mk(): i64[] { return [10000000000, 20000000000, 30000000000]; } function main(): i32 { let a: i64[] = mk(); let s: i64 = a[0] + a[2]; if (s > 35000000000) { return 5; } return 0; }`, 5},
		{"ret-direct-index", `function mk(): i64[] { return [10000000000, 20000000000, 30000000000]; } function main(): i32 { if (mk()[1] > 15000000000) { return 4; } return 0; }`, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("i64-array wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
