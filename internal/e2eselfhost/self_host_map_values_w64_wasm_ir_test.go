package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostMapValuesW64WasmIR covers `m.values()` on an i64/u64-VALUED map on
// the self-host WASM IR path (#5253 follow-up). The value column holds boxed
// 8-byte rc cells (see TestSelfHostMapI64ValueWasmIR); $__fern_map_values_w64
// snapshots the live cells and dereferences each into a fresh i64[]. Before this,
// a wide `.values()` kept the whole module on the legacy AST wasm emitter
// (module_has_wide_map_val_cached). Each case pipes a single program to the
// `wasm_ir_run -ir` driver (maps are builtins there), asserts the WAT reached
// $__fern_map_values_w64, then runs under wasmtime and checks the exit code.
func TestSelfHostMapValuesW64WasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-values-w64 wasm IR e2e")
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
		// SUM of wide values full-width. 5000000000 + 9000000000 + 12000000005 ==
		// 26000000005; % 1000 == 5 (a truncating i32 read would diverge).
		{"sum", `function main(): i32 { var m: Map[i32, i64] = Map { 1: 5000000000, 2: 9000000000, 3: 12000000005 }; var vs: i64[] = m.values(); var s: i64 = 0; var i: i32 = 0; while (i < vs.len()) { s = s + vs[i]; i = i + 1; } return (s % 1000) as i32; }`, 5},
		// COUNT of values() elements matches entry count. 4 entries → len 4.
		{"len", `function main(): i32 { var m: Map[i32, i64] = Map { 1: 10000000000, 5: 20000000000, 9: 30000000000, 13: 40000000000 }; return m.values().len(); }`, 4},
		// u64 values summed then shifted unsigned. Two copies of 18e18: their sum
		// wraps mod 2^64; >>63 of the wrapped sum. 18000000000000000000*2 mod 2^64
		// = 17553255926290448384; >>63 == 1.
		{"u64-sum-shift", `function main(): i32 { var m: Map[i32, u64] = Map { 1: 18000000000000000000 as u64, 2: 18000000000000000000 as u64 }; var vs: u64[] = m.values(); var s: u64 = 0; var i: i32 = 0; while (i < vs.len()) { s = s + vs[i]; i = i + 1; } return (s >> 63) as i32; }`, 1},
		// values() over a map_new'd + .insert map, with an OVERWRITE (key 1 set
		// twice) so only the live value is snapshotted. sum = 7000000000 (key 1's
		// final) + 3000000000 (key 2) = 10000000000; % 1000 == 0. Also guards no
		// over-release of the superseded cell (99).
		{"insert-overwrite", `function main(): i32 { var m: Map[i32, i64] = map_new(8); m = m.insert(1, 5000000000); m = m.insert(2, 3000000000); m = m.insert(1, 7000000000); var vs: i64[] = m.values(); var s: i64 = 0; var i: i32 = 0; while (i < vs.len()) { s = s + vs[i]; i = i + 1; } if (__rc_underflow() != 0) { return 99; } return (s % 1000) as i32; }`, 0},
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
			if !strings.Contains(string(wat), "$__fern_map_values_w64") {
				t.Errorf("%q did not reach the wide values() runtime (no $__fern_map_values_w64 in WAT)", tc.name)
			}
			watFile := filepath.Join(dir, "mapvw64_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("map values w64 wasm IR %q = %d, want %d (99 = cell over-release)", tc.name, got, tc.expected)
			}
		})
	}
}
