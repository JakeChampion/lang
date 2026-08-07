package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostOpt64bitIR is the correctness gate for f64/i64 Option/Result
// payloads on the wasm IR backend. The Option box used a 4-byte payload slot,
// so an i64/f64 payload was truncated; it now uses an 8-byte payload slot at
// offset 8 (matching the register backends) with the matching i64.store/load vs
// f64.store/load. Results pinned to hardcoded oracle values.
func TestSelfHostOpt64bitIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host 64bit-option wasm IR e2e")
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
		// Some(i64) -> match -> bound v is i64. 2e10 > 1.5e10 -> 7
		{"some-i64", `function main(): i32 { var r: Option[i64] = Some(20000000000); match (r) { Some(v) => { if (v > 15000000000) { return 7; } }, None => { return 0; } } return 0; }`, 7},
		// Some(f64) -> bound v is f64 (a 4-byte truncation fails). 2.5 > 2.0 -> 6
		{"some-f64", `function main(): i32 { var r: Option[f64] = Some(2.5); match (r) { Some(v) => { if (v > 2.0) { return 6; } }, None => { return 0; } } return 0; }`, 6},
		// None still discriminates correctly alongside the widened payload.
		{"none", `function main(): i32 { var r: Option[i64] = None; match (r) { Some(v) => { return 1; }, None => { return 8; } } return 0; }`, 8},
		// Result[i64, i32]: Ok(i64) payload. 9e9 + 2e9 = 1.1e10 > 1e10 -> 5
		{"ok-i64", `function main(): i32 { var r: Result[i64, i32] = Ok(9000000000); match (r) { Ok(v) => { var s: i64 = v + 2000000000; if (s > 10000000000) { return 5; } }, Err(e) => { return 0; } } return 0; }`, 5},
		// i64 payload used in arithmetic inside the arm.
		{"some-i64-arith", `function main(): i32 { var r: Option[i64] = Some(6000000000); match (r) { Some(v) => { var s: i64 = v + v; if (s > 11000000000) { return 9; } }, None => { return 0; } } return 0; }`, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("64bit-option wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
