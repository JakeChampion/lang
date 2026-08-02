package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostWasmMapWithoutDestructure pins the fix for #2933: on wasm,
// `var (m2, e) = m.without(k)` destructured the wrong value because
// $__fern_map_delete returned just the map box, not a [map, existed] tuple —
// so the destructure read box[0]/box[1] of the MAP (its keys/values pointers).
//
// The helper now returns a 2-element tuple box [map, existed] matching the
// register backends. Because the wasm AST and IR tuple consumers use different
// strides (element i at i*4 vs i*8), `existed` is written at BOTH offset 4 and
// offset 8; `map` (a 4-byte pointer) at offset 0 is i32-loaded as element 0 by
// either. Lifting the wasm-IR map_delete exclusion (a sibling of this fix) means
// these programs now lower through the IR path — where map-method dispatch on
// the destructured element (`m2.get_or` / `.len`) is type-tracked — so the
// whole-program use, not just `.len()`, is correct.
func TestSelfHostWasmMapWithoutDestructure(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm map .without e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// The issue's exact repro: removing a present key leaves len 1 (was 0).
		{"without-len", `function main(): i32 { var m: Map[string, i32] = Map { "a": 1, "b": 2 }; var (m2, e) = m.without("a"); return m2.len(); }`, 1},
		// The remaining map is still usable: the surviving value reads back.
		{"without-get-or", `function main(): i32 { var m: Map[string, i32] = Map { "a": 1, "b": 2 }; var (m2, e) = m.without("a"); return m2.get_or("b", -1); }`, 2},
		// existed == true when the key was present (the second tuple element).
		{"without-existed-true", `function main(): i32 { var m: Map[string, i32] = Map { "a": 1, "b": 2 }; var (m2, e) = m.without("a"); if (e) { return 7; } return 0; }`, 7},
		// existed == false (and the map is unchanged) when the key was absent.
		{"without-absent", `function main(): i32 { var m: Map[string, i32] = Map { "a": 1, "b": 2 }; var (m2, e) = m.without("zzz"); if (e) { return 99; } return m2.len(); }`, 2},
		// i32-keyed map: len + surviving value both correct after delete.
		{"without-i32-keys", `function main(): i32 { var m: Map[i32, i32] = Map { 1: 10, 2: 20 }; var (m2, e) = m.without(1); return m2.len() * 100 + m2.get_or(2, -1); }`, 120},
		// Deleting the only key empties the map; the gone key reads its default.
		{"without-to-empty", `function main(): i32 { var m: Map[string, i32] = Map { "k": 5 }; var (m2, e) = m.without("k"); if (m2.len() == 0) { return 42 - m2.get_or("k", 0); } return 0; }`, 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}
