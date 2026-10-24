package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostArrayBoundsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host array-bounds wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"read-past-end",
			`function main(): i32 { let a: i32[] = [1, 2, 3]; return a[5]; }`, 134},
		{"read-negative",
			`function main(): i32 { let a: i32[] = [1, 2, 3]; let i: i32 = 0 - 1; return a[i]; }`, 134},
		{"read-at-len",
			`function main(): i32 { let a: i32[] = [1, 2, 3]; return a[3]; }`, 134},
		{"write-past-end",
			`function main(): i32 { let a: i32[] = [1, 2, 3]; a = a.with(5, 9); return a[0]; }`, 134},
		{"in-range-ok",
			`function main(): i32 { let a: i32[] = [10, 20, 30]; a = a.with(1, 20); let s: i32 = 0; let i: i32 = 0; while (i < 3) { s = s + a[i]; i = i + 1; } return s; }`, 60},
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
				t.Fatalf("%s: driver failed: %v", tc.name, err)
			}
			if tc.want == 134 && !strings.Contains(string(wat), "unreachable") {
				t.Fatalf("%s: no `unreachable` trap in emitted wat — bounds check missing", tc.name)
			}
			watFile := filepath.Join(dir, "arr_bounds_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: wasmtime did not exit normally:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
