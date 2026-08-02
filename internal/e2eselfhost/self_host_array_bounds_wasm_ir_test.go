package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostArrayBoundsIRWasm — wasm counterpart of TestSelfHostArrayBoundsIR
// (x86-64) and TestSelfHostArrayBoundsIRArm64. It completes ARRAY-BOUNDS.md
// parity across all three self-host codegen backends: an out-of-range array
// index — read or write, over-large or negative — TRAPS instead of reading /
// writing past the end.
//
// Before this, wasm_ir.fern's arr_get / arr_set (kind_tag 88 / 89) formed the
// element address `base + 8 + idx*esz` with no length check, so the wasm
// self-host silently read/wrote past the end while the register backends
// aborted — a policy violation. The IR emit now checks the index against the
// length prefix (len @ base+0) with a single unsigned compare
// (`i32.le_u` of `len <= idx`, catching both negative and `idx >= len`) and
// `unreachable` on failure, which wasmtime surfaces as exit 134.
//
// In-range indexing (the "in-range-ok" leg) is unaffected — the sum probe
// walks the array and returns a real value.
func TestSelfHostArrayBoundsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host array-bounds wasm IR e2e")
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

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"read-past-end",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; return a[5]; }`, 134},
		{"read-negative",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; var i: i32 = 0 - 1; return a[i]; }`, 134},
		{"read-at-len",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; return a[3]; }`, 134},
		{"write-past-end",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; a = a.with(5, 9); return a[0]; }`, 134},
		// In-range: every element read + a write, no trap. 10+20+30 -> exit 60.
		{"in-range-ok",
			`function main(): i32 { var a: i32[] = [10, 20, 30]; a = a.with(1, 20); var s: i32 = 0; var i: i32 = 0; while (i < 3) { s = s + a[i]; i = i + 1; } return s; }`, 60},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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
