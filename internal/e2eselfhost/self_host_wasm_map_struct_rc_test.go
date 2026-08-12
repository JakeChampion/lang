package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcMapStructVal proves the map struct/enum-VALUE slice (native
// __drop_map_via_): a struct/enum-valued map's exit sweep routes to the
// per-type $__fern_map_release_via_<T>, which deep-releases each value through
// $__fern_release_<T> (the value's own fields / variant payload reclaim, not
// just its box). It also fixes the coupled value bug: a `var p = m.get_or(k,d)`
// binding now types as that struct, so field access resolves (it read 0
// before). Cross-checks value + the over-release detector.
func TestSelfHostRcMapStructVal(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("no wasmtime")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, _ := os.ReadFile(filepath.Join("../../examples/self_host", name))
		os.WriteFile(filepath.Join(dir, name), src, 0o644)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	cases := []struct {
		name string
		src  string
		exit int
	}{
		// value-correct: get_or struct field access now resolves (was 0).
		{"map-struct-val", "struct P { x: i32, y: i32 } function main(): i32 { var m = map_new_i32(8); m = m.insert(1, P { x: 40, y: 2 }); var p = m.get_or(1, P { x: 0, y: 0 }); return p.x + p.y + __rc_underflow_count(); }", 42},
		// DEEP release: struct value with a heap array field. 200k churn — the
		// struct box AND its array field reclaim each cycle via via_Inner ->
		// $__fern_release_Inner. Without deep release the arrays leak -> OOM.
		{"map-struct-deep-churn", "struct Inner { xs: i32[], n: i32 } function mk(): i32 { var m = map_new_i32(2); m = m.insert(1, Inner { xs: [1, 2, 3, 4], n: 5 }); m = m.insert(2, Inner { xs: [6, 7, 8, 9], n: 1 }); var p = m.get_or(1, Inner { xs: [0], n: 0 }); return p.n; } function main(): i32 { var k = 0; var s = 0; while (k < 200000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 5},
		// string-keyed + struct value: both key and value reclaim (via_ releases
		// keys too). value-correct + detector 0.
		{"map-strkey-structval", "struct P { x: i32, y: i32 } function main(): i32 { var m = map_new(8); var k: string = \"a\" + \"b\"; m = m.insert(k, P { x: 40, y: 2 }); var p = m.get_or(k, P { x: 0, y: 0 }); return p.x + p.y + __rc_underflow_count(); }", 42},
		// enum-valued map: variant payload (heap string) deep-released via
		// via_Shape -> $__fern_release_Shape dispatch. 200k churn, no OOM.
		{"map-enum-deep-churn", "enum Shape { Circle(string), Square(i32) } function mk(): i32 { var m = map_new_i32(2); m = m.insert(1, Circle(\"a\" + \"b\")); m = m.insert(2, Circle(\"c\" + \"d\")); return 2; } function main(): i32 { var k = 0; var s = 0; while (k < 200000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			os.WriteFile(watPath, wat, 0o644)
			cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: exited %d want %d\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}
