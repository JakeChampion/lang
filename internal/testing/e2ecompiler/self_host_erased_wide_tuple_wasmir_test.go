package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostErasedWideTupleWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping erased-wide tuple wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	const pair = `function pair[K, V](k: K, v: V): (K, V) { return (k, v); }`
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"i64-elem", pair + ` function main(): i32 { let t = pair(5000000000 as i64, 3); return (t.0 / 1000000000) as i32 + t.1; }`, 8},
		{"f64-elem", pair + ` function main(): i32 { let t = pair(2.5, 4); return (t.0 * 2.0) as i32 + t.1; }`, 9},
		{"string-elem", pair + ` function main(): i32 { let t = pair("xy", 3); return t.0.len() + t.1; }`, 5},
		{"destructure", pair + ` function main(): i32 { let (a, b) = pair(6000000000 as i64, 7); return (a / 1000000000) as i32 + b; }`, 13},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "tup_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s (an invalid module fails to load)", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("erased-wide tuple wasm IR %s = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
