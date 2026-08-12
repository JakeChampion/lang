package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostMapIterW64WasmIR covers `for (k, v) in m` over an i64/u64-VALUED map
// on the self-host WASM IR path (#5253 follow-up). The value column holds boxed
// 8-byte rc cells; $__fern_map_iter_w64 snapshots the values into a fresh i64[] and
// $__fern_mapiter_value_w64 reads each 8-byte element, with the loop's `v` (bound
// from it.value()) width-tracked i64 (infer_expr_width) and u64 (expr_is_u64).
// Before this, iterating a wide map kept the whole module on the legacy AST wasm
// emitter. Each case pipes a single program to the `wasm_ir_run -ir` driver (maps
// are builtins there), asserts the WAT reached $__fern_map_iter_w64, then runs
// under wasmtime. Values are cross-checked against the native interpreter.
//
// NOTE: the EXPLICIT `var it: MapIter[K, i64] = m.iter()` form is not covered — a
// pre-existing inference bug (a #5531 interaction: a map used via an explicit
// `.iter()` loses its i64 value-type tag and lowers the value column narrow) makes
// that form miscompile on wasm-IR both before and after this change, independent
// of the iteration runtime here. `for (k, v) in m` keeps the map's wide tag and
// lowers correctly. Fixing the explicit-iter inference is tracked separately.
func TestSelfHostMapIterW64WasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-iter-w64 wasm IR e2e")
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

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// for (k, v) in m: SUM of wide values full-width. 5e9 + 9e9 + 12000000005 =
		// 26000000005; % 1000 == 5.
		{"foreach-sum", `function main(): i32 { var m: Map[i32, i64] = Map { 1: 5000000000, 2: 9000000000, 3: 12000000005 }; var s: i64 = 0; for (k, v) in m { s = s + v; } return (s % 1000) as i32; }`, 5},
		// u64 values: OR-reduce over the column then unsigned shift. 18e18 has bit 63
		// set; OR keeps it; >>63 == 1.
		{"u64-foreach-shift", `function main(): i32 { var m: Map[i32, u64] = Map { 1: 18000000000000000000 as u64, 2: 1 as u64 }; var acc: u64 = 0; for (k, v) in m { acc = acc | v; } return (acc >> 63) as i32; }`, 1},
		// iterate a map_new'd + .insert map with an OVERWRITE (key 1 twice): only the
		// live value is snapshotted. 7e9 (key1 final) + 3e9 (key2) = 10e9; % 1000 == 0.
		// Also guards no over-release of superseded cells (99).
		{"insert-overwrite-iter", `function main(): i32 { var m: Map[i32, i64] = map_new(8); m = m.insert(1, 5000000000); m = m.insert(2, 3000000000); m = m.insert(1, 7000000000); var s: i64 = 0; for (k, v) in m { s = s + v; } if (__rc_underflow() != 0) { return 99; } return (s % 1000) as i32; }`, 0},
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
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !strings.Contains(string(wat), "$__fern_map_iter_w64") {
				t.Errorf("%q did not reach the wide iter runtime (no $__fern_map_iter_w64 in WAT)", tc.name)
			}
			watFile := filepath.Join(dir, "mapiw64_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("map iter w64 wasm IR %q = %d, want %d (99 = cell over-release)", tc.name, got, tc.expected)
			}
		})
	}
}
