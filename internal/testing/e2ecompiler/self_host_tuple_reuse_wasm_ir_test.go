package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostTupleReuseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host tuple-reuse wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"reuse-i32", `function main(): i32 { let a: (i32, i32) = (5, 7); let s: i32 = a.0 + a.1; let b: (i32, i32) = (2, 3); return s + b.0 + b.1; }`, 17},
		{"reuse-i64", `function main(): i32 { let a: (i64, i64) = (5, 7); let s: i64 = a.0 + a.1; let b: (i64, i64) = (30, 20); return (s + b.0 + b.1) as i32; }`, 62},
		{"reuse-f64", `function main(): i32 { let a: (f64, f64) = (1.5, 2.5); let s: f64 = a.0 + a.1; let b: (f64, f64) = (10.0, 3.0); return (s + b.0 + b.1) as i32; }`, 17},
		{"reuse-mixed", `function main(): i32 { let a: (i32, i64) = (5, 7); let s: i64 = (a.0 as i64) + a.1; let b: (i32, i64) = (2, 30); return (s + (b.0 as i64) + b.1) as i32; }`, 44},
		{"reuse-string", `function main(): i32 { let a: (string, i32) = ("hi", 5); let s: i32 = a.1; let b: (string, i32) = ("yo", 9); return s + b.1 + b.0.len(); }`, 16},
		{"donor-live", `function main(): i32 { let a: (i32, i32) = (5, 7); let b: (i32, i32) = (2, 3); return a.0 + a.1 + b.0 + b.1; }`, 17},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
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
				t.Errorf("tuple reuse wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
