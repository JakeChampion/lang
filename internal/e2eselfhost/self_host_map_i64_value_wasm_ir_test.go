package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostMapI64ValueWasmIR is the wasm port of TestSelfHostMapI64ValueIRX86_64
// (#5253): 64-bit Map VALUES (i64 / u64) on the self-host WASM IR path. The
// i32-celled wasm map runtime can't hold an 8-byte value inline, so a wide value
// is boxed into an 8-byte rc cell whose i32 pointer rides the value column
// ($__fern_map_set_w64 / $__fern_map_get_or_w64, map_w64_helpers), selected by the
// widekind==1 op flag. Before this, any i64/u64-valued map fell back to the legacy
// AST wasm emitter (module_has_wide_map_val_cached). Each case pipes a single
// program to the `wasm_ir_run -ir` driver (maps are builtins there), asserts the
// WAT reached the wide runtime, then runs under wasmtime and checks the exit code.
// (f64-valued maps and wide values()/get()/iteration stay deferred — not covered.)
func TestSelfHostMapI64ValueWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-i64-value wasm IR e2e")
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
		// (1) wide i64 LITERAL value round-trips full-width. 5000000007 % 1000 == 7
		// (a truncating i32 store would give 705032711 % 1000 == 711).
		{"i64-literal", `function main(): i32 { var m: Map[i32, i64] = Map { 1: 5000000007 }; var g: i64 = m.get_or(1, 0); return (g % 1000) as i32; }`, 7},
		// (2) u64 CAST value + chained unsigned shift. 18e18 >> 58 == 62.
		{"u64-cast-shift", `function main(): i32 { var m: Map[i32, u64] = Map { 1: 18000000000000000000 as u64 }; return (m.get_or(1, 0 as u64) >> 58) as i32; }`, 62},
		// (3) i64 value from a VARIABLE, inserted via .insert on a map_new'd map.
		// 9000000000 % 1000 == 0.
		{"i64-var-insert", `function main(): i32 { var m: Map[i32, i64] = map_new(8); var v: i64 = 9000000000; m = m.insert(1, v); return (m.get_or(1, 0) % 1000) as i32; }`, 0},
		// (4) u64 value inserted via .insert (cast), chained shift. Same 62.
		{"u64-insert-shift", `function main(): i32 { var m: Map[i32, u64] = map_new(8); m = m.insert(1, 18000000000000000000 as u64); return (m.get_or(1, 0 as u64) >> 58) as i32; }`, 62},
		// (5) unannotated get_or binding width-tracks i64, so a later `% 1000` is
		// 64-bit. 12000000005 % 1000 == 5.
		{"i64-unannotated-getor", `function main(): i32 { var m: Map[i32, i64] = Map { 2: 12000000005 }; var g = m.get_or(2, 0); return (g % 1000) as i32; }`, 5},
		// (6) MISS path returns the (wide) default full-width. 7000000009 % 1000 == 9.
		{"i64-default-miss", `function main(): i32 { var m: Map[i32, i64] = map_new(8); var g: i64 = m.get_or(99, 7000000009); return (g % 1000) as i32; }`, 9},
		// (7) OVERWRITE churn: 200 inserts on the same key free 199 superseded cells;
		// a double-free of a cell ticks the underflow detector → 99. Final value
		// 199*3000000000 % 1000 == 0.
		{"i64-overwrite-churn", `function main(): i32 { var m: Map[i32, i64] = map_new(8); var i: i32 = 0; while (i < 200) { m = m.insert(1, (i as i64) * 3000000000); i = i + 1; } if (__rc_underflow() != 0) { return 99; } return (m.get_or(1, 0) % 1000) as i32; }`, 0},
		// (8) BUILD-AND-DROP reclaim: a wide map built + dropped per call across 300
		// iterations; every cell freed exactly once (99 = over-release, 88 = wrong
		// value). m.get_or(2,·) == 2*5000000000 == 10000000000.
		{"i64-build-drop-reclaim", `function build(): i32 { var m: Map[i32, i64] = map_new(8); var i: i32 = 0; while (i < 8) { m = m.insert(i, (i as i64) * 5000000000); i = i + 1; } if (m.get_or(2, 0) != 10000000000) { return 1; } return 0; } function main(): i32 { var bad: i32 = 0; var k: i32 = 0; while (k < 300) { if (build() != 0) { bad = 1; } k = k + 1; } if (__rc_underflow() != 0) { return 99; } if (bad != 0) { return 88; } return 0; }`, 0},
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
			if !strings.Contains(string(wat), "$__fern_map_set_w64") {
				t.Errorf("%q did not reach the wide-map IR runtime (no $__fern_map_set_w64 in WAT)", tc.name)
			}
			watFile := filepath.Join(dir, "mapi64_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("map i64 value wasm IR %q = %d, want %d (99 = cell over-release, 88 = wrong value)", tc.name, got, tc.expected)
			}
		})
	}
}
