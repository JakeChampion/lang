package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostI64ArrayIR is the correctness gate for i64 arrays on the wasm IR
// backend. Like the f64-array test it pins each program's IR result to a
// hardcoded oracle (the wasm AST backend's i64-array layout differs, so this is
// not an AST-vs-IR differential). First i64-array slice: literals, indexed read,
// and i64[] params; writes / for-in / returns / slices stay on the AST path.
func TestSelfHostI64ArrayIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host i64-array wasm IR e2e")
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
		// literal + indexed read: 5e9 + 6e9 = 11e9 > 1e10 -> 7
		{"read", `function main(): i32 { var a: i64[] = [5000000000, 6000000000]; var s: i64 = a[0] + a[1]; if (s > 10000000000) { return 7; } return 0; }`, 7},
		// small i64 values still store/load 8 bytes: a[0]+a[1] = 3 -> 3
		{"read-small", `function main(): i32 { var a: i64[] = [1, 2, 3]; var s: i64 = a[0] + a[1]; return s as i32; }`, 3},
		// counted read loop accumulating i64: 1e10+2e10+3e10 = 6e10 > 5e10 -> 9
		{"loop", `function main(): i32 { var a: i64[] = [10000000000, 20000000000, 30000000000]; var s: i64 = 0; var i = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } if (s > 50000000000) { return 9; } return 0; }`, 9},
		// i64[] param: a[0]+a[1] = 11e9 > 1e10 -> 5
		{"param", `function sum(a: i64[]): i64 { return a[0] + a[1]; } function main(): i32 { var arr: i64[] = [5000000000, 6000000000]; var r: i64 = sum(arr); if (r > 10000000000) { return 5; } return 0; }`, 5},
		// indexed write: a[1] = 9e9; a[0]+a[1] = 1e9+9e9 = 1e10; > 9.9e9 -> 8
		{"write", `function main(): i32 { var a: i64[] = [1000000000, 2000000000]; a[1] = 9000000000; var s: i64 = a[0] + a[1]; if (s > 9900000000) { return 8; } return 0; }`, 8},
		// write in a loop: fill a[i] = i*1e10, then sum = 0+1e10+2e10 = 3e10 > 2.5e10 -> 6
		{"write-loop", `function main(): i32 { var a: i64[] = [0, 0, 0]; var i = 0; while (i < 3) { a[i] = (i as i64) * 10000000000; i = i + 1; } var s: i64 = a[0] + a[1] + a[2]; if (s > 25000000000) { return 6; } return 0; }`, 6},
		// for-in: element x is an 8-byte i64. 1e10+2e10+3e10 = 6e10 > 5e10 -> 9
		{"forin", `function main(): i32 { var a: i64[] = [10000000000, 20000000000, 30000000000]; var s: i64 = 0; for x in a { s = s + x; } if (s > 50000000000) { return 9; } return 0; }`, 9},
		// for-in with body comparison: count elements > 1.5e10 -> 2
		{"forin-cmp", `function main(): i32 { var a: i64[] = [10000000000, 20000000000, 30000000000, 5000000000]; var c = 0; for x in a { if (x > 15000000000) { c = c + 1; } } return c; }`, 2},
		// i64[] slice (8-byte element copy via arr_slice8): b = a[1:3]; b[0]+b[1] = 2e10+3e10 = 5e10 > 4e10 -> 7
		{"slice", `function main(): i32 { var a: i64[] = [10000000000, 20000000000, 30000000000, 40000000000]; var b = a[1:3]; var s: i64 = b[0] + b[1]; if (s > 40000000000) { return 7; } return 0; }`, 7},
		// i64[] slice length: a[1:3].len() = 2
		{"slice-len", `function main(): i32 { var a: i64[] = [10000000000, 20000000000, 30000000000, 40000000000]; var b = a[1:3]; return b.len(); }`, 2},
		// direct index of a slice expression, no intermediate local: a[1:3] =
		// [2e10, 3e10]; [1] = 3e10 > 2.5e10 -> 6. The 8-byte i64 stride must
		// survive when the index base is a fresh slice view rather than a named
		// array local — the i64 sibling of the f64 #2908 bug.
		{"slice-direct-index", `function main(): i32 { var a: i64[] = [10000000000, 20000000000, 30000000000, 40000000000]; if (a[1:3][1] > 25000000000) { return 6; } return 0; }`, 6},
		// i64[]-returning function (move-on-return): caller element-types the
		// result as i64[]. a[0]+a[2] = 1e10+3e10 = 4e10 > 3.5e10 -> 5
		{"ret", `function mk(): i64[] { return [10000000000, 20000000000, 30000000000]; } function main(): i32 { var a: i64[] = mk(); var s: i64 = a[0] + a[2]; if (s > 35000000000) { return 5; } return 0; }`, 5},
		// direct index of an i64[]-returning call: mk()[1] = 2e10; > 1.5e10 -> 4
		{"ret-direct-index", `function mk(): i64[] { return [10000000000, 20000000000, 30000000000]; } function main(): i32 { if (mk()[1] > 15000000000) { return 4; } return 0; }`, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("i64-array wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
