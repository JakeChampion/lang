package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostMapF64ValueWasmIR covers f64-VALUED maps on the self-host WASM IR
// path (#5253 follow-up). Before this, an f64-valued map correctly DEFERRED (its
// set op carries widekind 2) — but to the legacy AST wasm emitter, which
// MISCOMPILED it: the shared i32-celled $__fern_map_set was called with an f64
// value, producing invalid WAT ("failed to compile"). The fix lowers f64 maps on
// the IR path via the same boxed 8-byte rc cells as i64/u64: the cell stores raw
// bytes, so get/values/get_or/iter reuse the *_w64 helpers with an f64<->i64
// reinterpret at the scalar set value / get_or default+result / mapiter value.
// Each case pipes a single program to the `wasm_ir_run -ir` driver (maps are
// builtins there), asserts the WAT reached the wide runtime, then runs under
// wasmtime. Values cross-checked against the native interpreter.
func TestSelfHostMapF64ValueWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-f64-value wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// get_or HIT: the f64 value round-trips full-width. 2.5 * 2.0 == 5.0 → 5.
		{"get_or-hit", `function main(): i32 { var m: Map[i32, f64] = Map { 1: 2.5 }; return (m.get_or(1, 0.0) * 2.0) as i32; }`, 5},
		// get_or MISS: the f64 default is returned. 3.0 → 3.
		{"get_or-miss", `function main(): i32 { var m: Map[i32, f64] = Map { 1: 2.5 }; return (m.get_or(9, 3.0)) as i32; }`, 3},
		// values(): f64[] snapshot summed. 1.5 + 2.5 == 4.0 → 4.
		{"values-sum", `function main(): i32 { var m: Map[i32, f64] = Map { 1: 1.5, 2: 2.5 }; var vs: f64[] = m.values(); var s: f64 = 0.0; var i: i32 = 0; while (i < vs.len()) { s = s + vs[i]; i = i + 1; } return s as i32; }`, 4},
		// get(): Some(v) carries the full-width f64 through the Option[f64] payload.
		// 2.5 * 2.0 == 5.0 → 5.
		{"get-some", `function main(): i32 { var m: Map[i32, f64] = Map { 1: 2.5 }; match (m.get(1)) { Some(v) => { return (v * 2.0) as i32; }, None => { return 0; } } }`, 5},
		// for (k, v) in m: f64 values summed. 1.5 + 2.5 + 4.0 == 8.0 → 8.
		{"foreach-sum", `function main(): i32 { var m: Map[i32, f64] = Map { 1: 1.5, 2: 2.5, 3: 4.0 }; var s: f64 = 0.0; for (k, v) in m { s = s + v; } return s as i32; }`, 8},
		// OVERWRITE reclaim: key 1 set twice; only the live value read; no cell
		// over-release (99). 7.0 * 2.0 == 14.0 → 14.
		{"overwrite-reclaim", `function main(): i32 { var m: Map[i32, f64] = map_new(8); m = m.insert(1, 2.5); m = m.insert(1, 7.0); if (__rc_underflow() != 0) { return 99; } return (m.get_or(1, 0.0) * 2.0) as i32; }`, 14},
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
			if !strings.Contains(string(wat), "$__fern_map_set_w64") && !strings.Contains(string(wat), "$__fern_map_get_or_w64") {
				t.Errorf("%q did not reach the wide-map IR runtime (no *_w64 in WAT)", tc.name)
			}
			watFile := filepath.Join(dir, "mapf64_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("map f64 value wasm IR %q = %d, want %d (99 = cell over-release)", tc.name, got, tc.expected)
			}
		})
	}
}
