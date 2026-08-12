package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostI64StructFieldIR is the correctness gate for i64 struct fields on
// the wasm IR backend. i64 struct fields were not leaf-safe (so any struct with
// one bailed); they now lower with an 8-byte i64 slot (i64.load / i64.store,
// distinct from f64.load/store), mirroring the f64 struct field work (#2721)
// plus the i64-array i64/f64 disambiguation. This unblocks i64 methods next.
// Results pinned to hardcoded oracle values.
func TestSelfHostI64StructFieldIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host i64-struct-field wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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
		// read an i64 field (8-byte). c.base = 2e10 > 1.5e10 -> 7
		{"read", `struct C { base: i64 } function main(): i32 { var c = C { base: 20000000000 }; var b: i64 = c.base; if (b > 15000000000) { return 7; } return 0; }`, 7},
		// small i64 field still round-trips through 8 bytes. base = 3 -> 3
		{"read-small", `struct C { base: i64 } function main(): i32 { var c = C { base: 3 }; return c.base as i32; }`, 3},
		// mixed struct: i32 + i64 + i32 fields, offsets stay 8-byte stride.
		// a(=1) + b(=5e9>4e9?1) ... return v.a + v.c with i64 in the middle.
		{"mixed-fields", `struct V { a: i32, big: i64, c: i32 } function main(): i32 { var v = V { a: 3, big: 9000000000, c: 4 }; var s: i64 = v.big * 2; if (s > 17000000000) { return v.a + v.c; } return 0; }`, 7},
		// i64 field write: c.base = 8e9; c.base + 3e9 = 1.1e10 > 1e10 -> 5
		{"write", `struct C { base: i64 } function main(): i32 { var c = C { base: 1000000000 }; c.base = 8000000000; var s: i64 = c.base + 3000000000; if (s > 10000000000) { return 5; } return 0; }`, 5},
		// i64 field in an arithmetic chain (field read feeds lower_i64).
		{"arith", `struct C { x: i64, y: i64 } function main(): i32 { var c = C { x: 6000000000, y: 7000000000 }; var s: i64 = c.x + c.y; if (s > 12000000000) { return 6; } return 0; }`, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("i64-struct-field wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
