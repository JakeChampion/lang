package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostEnum64bitIR is the correctness gate for f64/i64 enum-variant
// payloads on the wasm IR backend. Enum variants are built/extracted via the
// struct machinery; the variant payload field now reads/writes at 8-byte width
// (struct_get_i64 / struct_get width 64) for an i64/f64 payload, with the value
// lowered via lower_i64. Completes 64-bit types across every payload container.
// Results pinned to hardcoded oracle values.
func TestSelfHostEnum64bitIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host 64bit-enum wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
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
		// enum with an i64 payload: A(2e10), match binds n:i64. 2e10 > 1.5e10 -> 7
		{"i64-payload", `enum E { A(i64), B } function f(e: E): i32 { match (e) { A(n) => { if (n > 15000000000) { return 7; } return 1; }, B => { return 3; } } return 0; } function main(): i32 { return f(A(20000000000)); }`, 7},
		// the no-payload variant still discriminates alongside the 8-byte payload.
		{"i64-other-variant", `enum E { A(i64), B } function f(e: E): i32 { match (e) { A(n) => { return 1; }, B => { return 3; } } return 0; } function main(): i32 { return f(B); }`, 3},
		// enum with an f64 payload (a 4-byte truncation fails). 2.5 > 2.0 -> 6
		{"f64-payload", `enum E { V(f64), W } function f(e: E): i32 { match (e) { V(x) => { if (x > 2.0) { return 6; } return 1; }, W => { return 3; } } return 0; } function main(): i32 { return f(V(2.5)); }`, 6},
		// i64 payload used in arithmetic inside the arm.
		{"i64-arith", `enum E { N(i64) } function f(e: E): i32 { match (e) { N(v) => { var s: i64 = v + v; if (s > 11000000000) { return 9; } return 1; } } return 0; } function main(): i32 { return f(N(6000000000)); }`, 9},
		// A u64 payload (bit 63 set) bound + shifted UNSIGNED: the payload reads at
		// 8-byte width (struct_get_i64, matching the mark_u64 binding — else the
		// wasm module fails to validate) and `x >> 57` selects shr_u, so
		// 0xF9CCD8A1C5080000 >> 57 == 124 → 5. A signed read/shift would diverge.
		{"u64-payload-shift", `enum E { U(u64), N } function f(e: E): i32 { match (e) { U(x) => { if (x >> 57 == 124) { return 5; } return 1; }, N => { return 3; } } return 0; } function main(): i32 { return f(U(18000000000000000000)); }`, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("64bit-enum wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
