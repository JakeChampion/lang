package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostMapGetW64WasmIR covers `m.get(k)` → Option[i64/u64] on the self-host
// WASM IR path (#5253 follow-up). The value column holds boxed 8-byte rc cells;
// $__fern_map_get_w64 dereferences the cell into a 16-byte Option box
// [tag@0][payload@8] — the same shape opt_make/opt_payload use for an 8-byte
// payload — so a downstream `match` reads the value full-width. Before this, a wide
// `.get()` kept the whole module on the legacy AST wasm emitter. Each case pipes a
// single program to the `wasm_ir_run -ir` driver (maps are builtins there), asserts
// the WAT reached $__fern_map_get_w64, then runs under wasmtime.
func TestSelfHostMapGetW64WasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-get-w64 wasm IR e2e")
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
		name     string
		src      string
		expected int
	}{
		// HIT path: Some(v) carries the full-width i64. 5000000007 % 1000 == 7.
		{"hit", `function main(): i32 { var m: Map[i32, i64] = Map { 1: 5000000007 }; match (m.get(1)) { Some(v) => { return (v % 1000) as i32; }, None => { return 42; } } }`, 7},
		// MISS path: None → the fallback arm. Returns 42.
		{"miss", `function main(): i32 { var m: Map[i32, i64] = Map { 1: 5000000007 }; match (m.get(9)) { Some(v) => { return (v % 1000) as i32; }, None => { return 42; } } }`, 42},
		// u64 value through Some, unsigned shift. 18e18 >> 58 == 62.
		{"u64-hit-shift", `function main(): i32 { var m: Map[i32, u64] = Map { 1: 18000000000000000000 as u64 }; match (m.get(1)) { Some(v) => { return (v >> 58) as i32; }, None => { return 0; } } }`, 62},
		// HIT after a map_new'd + .insert, value from a variable. 9000000000 % 1000 == 0.
		{"insert-hit", `function main(): i32 { var m: Map[i32, i64] = map_new(8); var x: i64 = 9000000000; m = m.insert(3, x); match (m.get(3)) { Some(v) => { return (v % 1000) as i32; }, None => { return 7; } } }`, 0},
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
			if !strings.Contains(string(wat), "$__fern_map_get_w64") {
				t.Errorf("%q did not reach the wide get() runtime (no $__fern_map_get_w64 in WAT)", tc.name)
			}
			watFile := filepath.Join(dir, "mapgw64_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("map get w64 wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
