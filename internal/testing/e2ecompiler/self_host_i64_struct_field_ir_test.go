package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostI64StructFieldIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host i64-struct-field wasm IR e2e")
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
		{"read", `struct C { base: i64 } function main(): i32 { let c = C { base: 20000000000 }; let b: i64 = c.base; if (b > 15000000000) { return 7; } return 0; }`, 7},
		{"read-small", `struct C { base: i64 } function main(): i32 { let c = C { base: 3 }; return c.base as i32; }`, 3},
		{"mixed-fields", `struct V { a: i32, big: i64, c: i32 } function main(): i32 { let v = V { a: 3, big: 9000000000, c: 4 }; let s: i64 = v.big * 2; if (s > 17000000000) { return v.a + v.c; } return 0; }`, 7},
		{"write", `struct C { base: i64 } function main(): i32 { let c = C { base: 1000000000 }; c = C { ...c, base: 8000000000 }; let s: i64 = c.base + 3000000000; if (s > 10000000000) { return 5; } return 0; }`, 5},
		{"arith", `struct C { x: i64, y: i64 } function main(): i32 { let c = C { x: 6000000000, y: 7000000000 }; let s: i64 = c.x + c.y; if (s > 12000000000) { return 6; } return 0; }`, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("i64-struct-field wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
