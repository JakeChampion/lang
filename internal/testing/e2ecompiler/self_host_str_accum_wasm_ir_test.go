package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostStrAccumWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host string-accumulator wasm IR e2e")
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
		{"accum-churn", `function churn(n: i32): i32 { let x: string = "yy"; let s: string = ""; let i: i32 = 0; while (i < n) { s = s + x; if (s.len() > 40) { s = x.to_ascii_upper(); } i = i + 1; } return 0; } function main(): i32 { let r: i32 = churn(3000); if (__rc_underflow_count() != 0) { return 99; } return r; }`, 0},
		{"accum-len", `function build(): i32 { let s: string = "a"; let i: i32 = 0; while (i < 3) { s = s + "b"; i = i + 1; } return s.len(); } function main(): i32 { let r: i32 = build(); if (__rc_underflow_count() != 0) { return 99; } return r; }`, 4},
		{"accum-return-churn", `function build(n: i32): string { let x: string = "z"; let s: string = ""; let i: i32 = 0; while (i < n) { s = s + x; i = i + 1; } return s; } function main(): i32 { let t: i32 = 0; let k: i32 = 0; while (k < 500) { let r: string = build(30); t = (t + r.len()) % 7; k = k + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
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
			if !strings.Contains(string(wat), "$__fern_str_box") {
				t.Errorf("%q did not reach the IR box path (no box in WAT)", tc.name)
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
				t.Errorf("string accumulator wasm IR %q = %d, want %d (99 = double-free detected)", tc.name, got, tc.expected)
			}
		})
	}
}
