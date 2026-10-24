package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWasmSelfHostRandom(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm random e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	cases := []struct {
		name   string
		source string
		exit   int
	}{
		{"random-bytes-len", "function main(): i32 { let b = random_bytes(8); return b.len(); }", 8},
		{"random-bytes-zero", "function main(): i32 { let b = random_bytes(0); return b.len(); }", 0},
		{"random-bytes-range", "function main(): i32 { let b = random_bytes(100); for x in b { if (x < 0) { return 1; } if (x > 255) { return 2; } } return 42; }", 42},
		{"random-bytes-index", "function main(): i32 { let b = random_bytes(4); let x = b[0]; if (x >= 0 && x <= 255) { return 7; } return 1; }", 7},
		{"random-i32-runs", "function main(): i32 { let x = random_i32(); let y = x & 255; if (y >= 0 && y <= 255) { return 9; } return 1; }", 9},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.source))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(t.TempDir(), "prog.wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}
