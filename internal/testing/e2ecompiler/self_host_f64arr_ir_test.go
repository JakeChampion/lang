package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostF64ArrayWasmIR is the CORRECTNESS gate for f64 arrays on the wasm
// IR backend (wasm_ir.fern). Each program's result is pinned to a hardcoded
// oracle value (the interpreter's / hand-computed answer), so an element stored
// at a 4-byte stride — truncating the f64 — shows up as a wrong answer.
//
// Coverage: f64 array literal + indexed read, indexed write (a[i] = v), a counted
// read loop, for-in iteration, an f64[] param, expression-valued elements, and
// f64[]-returning functions (bound + directly indexed) — every f64-array shape
// the IR lowers (arr_make / arr_get / arr_set width 64 -> 8-byte stride +
// f64.load/store on wasm).
func TestSelfHostF64ArrayWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host f64-array wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	// runIR pipes src to the driver,
	// runs the emitted WAT under wasmtime, returns the exit code.
	runIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
		// literal + indexed read: 1.5 + 2.5 = 4.0 > 3.0 -> 7
		{"read", `function main(): i32 { let a: f64[] = [1.5, 2.5]; let x: f64 = a[0] + a[1]; if (x > 3.0) { return 7; } return 0; }`, 7},
		// indexed write: a[1] = 5.5; 1.0 + 5.5 = 6.5 > 6.0 -> 8
		{"write", `function main(): i32 { let a: f64[] = [1.0, 2.0]; a = a.with(1, 5.5); let x: f64 = a[0] + a[1]; if (x > 6.0) { return 8; } return 0; }`, 8},
		// counted read loop: 1.5 + 2.5 + 3.0 = 7.0 > 6.0 -> 9
		{"loop", `function main(): i32 { let a: f64[] = [1.5, 2.5, 3.0]; let s: f64 = 0.0; let i = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } if (s > 6.0) { return 9; } return 0; }`, 9},
		// for-in iteration: the element binding x is an 8-byte f64. 1.5+2.5+3.0 = 7.0 > 6.0 -> 9
		{"forin", `function main(): i32 { let a: f64[] = [1.5, 2.5, 3.0]; let s: f64 = 0.0; for x in a { s = s + x; } if (s > 6.0) { return 9; } return 0; }`, 9},
		// for-in with a body comparison: count elements > 2.0 -> 2
		{"forin-cmp", `function main(): i32 { let a: f64[] = [1.0, 2.5, 3.5, 0.5]; let c = 0; for x in a { if (x > 2.0) { c = c + 1; } } return c; }`, 2},
		// f64[] param: 2.5 + 4.0 = 6.5 > 6.0 -> 5
		{"param", `function sum(a: f64[]): f64 { return a[0] + a[1]; } function main(): i32 { let arr: f64[] = [2.5, 4.0]; let r: f64 = sum(arr); if (r > 6.0) { return 5; } return 0; }`, 5},
		// expression-valued elements: a = [2.0, 4.0, 3.0]; a[1] + a[2] = 7.0 > 6.0 -> 6
		{"expr-elems", `function main(): i32 { let k: f64 = 2.0; let a: f64[] = [k, k * 2.0, k + 1.0]; let x: f64 = a[1] + a[2]; if (x > 6.0) { return 6; } return 0; }`, 6},
		// mixed-precision: read an f64 element, cast to i32. a[2] = 9.5 -> 9
		{"read-cast", `function main(): i32 { let a: f64[] = [7.5, 8.5, 9.5]; return a[2] as i32; }`, 9},
		// f64[]-returning function (move-on-return): caller element-width-tracks
		// the result as f64[]. a[0]+a[2] = 1.5+3.5 = 5.0 -> 5
		{"ret", `function mk(): f64[] { return [1.5, 2.5, 3.5]; } function main(): i32 { let a: f64[] = mk(); let s: f64 = a[0] + a[2]; return s as i32; }`, 5},
		// direct index of an f64[]-returning call: mk()[1] = 2.5 -> 2
		{"ret-direct-index", `function mk(): f64[] { return [1.5, 2.5, 3.5]; } function main(): i32 { return mk()[1] as i32; }`, 2},
		// f64[] slice (8-byte element copy): b = a[1:3] = [2.5, 3.5]; sum = 6.0 -> 6.
		// (b is left unannotated: `a[i:j]` yields a non-owning slice view `[f64]`,
		// which the native checker intentionally won't assign to an *owning* `f64[]`
		// — a redundant `: f64[]` annotation would be a view->owning mismatch. The
		// self-host slice copies into a fresh owning array regardless.)
		{"slice", `function main(): i32 { let a: f64[] = [1.5, 2.5, 3.5, 4.5]; let b = a[1:3]; let s: f64 = b[0] + b[1]; return s as i32; }`, 6},
		// f64[] slice length: a[1:3].len() = 2
		{"slice-len", `function main(): i32 { let a: f64[] = [1.5, 2.5, 3.5, 4.5]; let b = a[1:3]; return b.len(); }`, 2},
		// direct index of an f64[] slice: a[1:4][1] = 3.5 -> 3
		{"slice-direct-index", `function main(): i32 { let a: f64[] = [1.5, 2.5, 3.5, 4.5]; return a[1:4][1] as i32; }`, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("f64-array wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
