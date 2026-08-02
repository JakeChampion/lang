package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostTuple64bitIR is the correctness gate for f64/i64 tuple elements on
// the wasm IR backend. Tuples previously bailed any module whose tuple had an i64
// element (and silently 4-byte-truncated an f64 one); they now use a uniform
// 8-byte element slot with the matching i64.store/load vs f64.store/load
// (register backends already used 8-byte slots). Rounds out 64-bit types across
// all composites. Results pinned to hardcoded oracle values.
func TestSelfHostTuple64bitIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host 64bit-tuple wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	runIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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
		// i64 tuple element via .N: t.0 = 2e10 > 1.5e10 -> 7
		{"i64-dotN", `function main(): i32 { var t = (20000000000, 3); var b: i64 = t.0; if (b > 15000000000) { return 7; } return 0; }`, 7},
		// mixed (i64, i32) tuple: i32 element at .1 stays correct alongside the i64.
		{"mixed-i32", `function main(): i32 { var t = (9000000000, 4); return t.1; }`, 4},
		// f64 tuple element via .N: t.1 = 2.5; > 2.0 -> 6 (previously 4-byte-truncated)
		{"f64-dotN", `function main(): i32 { var t = (1, 2.5); var f: f64 = t.1; if (f > 2.0) { return 6; } return 0; }`, 6},
		// i64 destructure: var (a, b) = (5e9, 6e9); a + b = 11e9 > 1e10 -> 5
		{"i64-destructure", `function main(): i32 { var (a, b) = (5000000000, 6000000000); var s: i64 = a + b; if (s > 10000000000) { return 5; } return 0; }`, 5},
		// i64 tuple element in arithmetic (field read feeds lower_i64).
		{"i64-arith", `function main(): i32 { var t = (6000000000, 7000000000); var s: i64 = t.0 + t.1; if (s > 12000000000) { return 9; } return 0; }`, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("64bit-tuple wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
