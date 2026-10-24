package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWasmSelfHostF64ToI64(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm f64-to-i64 e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	cases := []struct {
		name   string
		source string
		stdout string
	}{
		{"via-local", "function main(): i32 { let x: f64 = 5000000000.0; let r: i64 = x as i64; print_i64(r); return 0; }", "5000000000"},
		{"direct-print", "function main(): i32 { print_i64(9000000000.0 as i64); return 0; }", "9000000000"},
		{"truncates", "function main(): i32 { let x: f64 = 3.9; print_i64(x as i64); return 0; }", "3"},
		{"negative", "function main(): i32 { let x: f64 = -4000000000.0; print_i64(x as i64); return 0; }", "-4000000000"},
		{"expr", "function main(): i32 { let a: f64 = 2500000000.0; print_i64((a * 2.0) as i64); return 0; }", "5000000000"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wat := runCapture(t, gcc, runner, driverBin, []byte(withPrintInt(tc.source)))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(t.TempDir(), "prog.wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			out, _ := cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s: wasm exited %d, want 0\n--- WAT ---\n%s", tc.name, code, wat)
			}
			if string(out) != tc.stdout {
				t.Errorf("%s: wasm stdout = %q, want %q", tc.name, string(out), tc.stdout)
			}
		})
	}
}
