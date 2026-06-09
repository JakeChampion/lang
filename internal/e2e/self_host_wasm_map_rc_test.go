package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcMapGrowWasm proves the map keys/vals/used arrays are now
// rc-boxed (via $__fern_str_box — flat, so [arr + i*4] addressing is
// unchanged) and that $__fern_map_grow FREES the old arrays after rehashing
// instead of leaking them on every resize. Previously a map that grew N times
// leaked 3*N raw arrays. Values stay correct (the string key/value pointers
// are copied into the new arrays before the old buffers are freed) and the
// over-release detector stays 0 (the old buffers are freed flat — the moved
// string elements survive in the new arrays). The map box itself + its final
// arrays are still freed by the map-free slice (later); this is the
// grow-leak fix + the array-layout foundation.
func TestSelfHostRcMapGrowWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm map-grow e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "wasm.fern", "wasm_run.fern"} {
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
		// String-keyed map grown from cap 2 by 5 distinct keys (forces a
		// grow+rehash): values intact + detector clean.
		{"map-grow-str-keys", "function main(): i32 { var m = map_new(2); m = m.insert(\"a\", 10); m = m.insert(\"b\", 20); m = m.insert(\"c\", 30); m = m.insert(\"d\", 13); m = m.insert(\"e\", 21); return m.get_or(\"c\", -1) + m.get_or(\"d\", -1) + __fern_rc_underflow_count(); }", 43},
		// i32-keyed map grown by 100 distinct keys (several resizes): a probed
		// lookup is value-correct + detector clean.
		{"map-grow-i32-keys", "function main(): i32 { var m = map_new_i32(2); var k = 0; while (k < 100) { m = m.insert(k, k); k = k + 1; } return m.get_or(50, -1) + m.get_or(70, -1) + __fern_rc_underflow_count(); }", 120},
		// A churn: build a growing map each iteration and drop it. The grow path
		// reclaims old arrays so memory doesn't blow up; detector clean.
		{"map-grow-churn", "function mk(): i32 { var m = map_new_i32(2); var k = 0; while (k < 30) { m = m.insert(k, k); k = k + 1; } return m.get_or(25, -1); } function main(): i32 { var n = 0; var k = 0; while (k < 5000) { n = mk(); k = k + 1; } return (n % 100) + __fern_rc_underflow_count(); }", 25},
		// String values that survive the array reallocation on grow.
		{"map-grow-str-vals", "function main(): i32 { var m = map_new(2); m = m.insert(\"x\", 1); m = m.insert(\"y\", 2); m = m.insert(\"z\", 3); m = m.insert(\"w\", 4); return m.get_or(\"x\", -1) + m.get_or(\"w\", -1) + m.len() + __fern_rc_underflow_count(); }", 9},
		// COUNTING milestone (free off): an owned map local is released (rc dec)
		// at exit, value-correct + detector clean.
		{"map-swept-clean", "function main(): i32 { var m = map_new_i32(8); m = m.insert(3, 40); return m.get_or(3, -1) + 2 + __fern_rc_underflow_count(); }", 42},
		// A string-keyed map swept at exit (the box dec'd; arrays + strings leak
		// soundly while free is off) — value-correct + detector clean.
		{"map-str-swept-clean", "function main(): i32 { var m = map_new(8); m = m.insert(\"k\", 40); return m.get_or(\"k\", -1) + 2 + __fern_rc_underflow_count(); }", 42},
		// A map re-built each loop iteration: detector stays clean (counting;
		// free off, so intermediates + the grow-freed arrays don't over-release).
		{"map-counting-churn-clean", "function mk(): i32 { var m = map_new(2); m = m.insert(\"a\", 1); m = m.insert(\"b\", 2); m = m.insert(\"c\", 3); m = m.insert(\"d\", 4); return m.get_or(\"c\", -1); } function main(): i32 { var k = 0; var s = 0; while (k < 5000) { s = mk(); k = k + 1; } return (s % 100) + __fern_rc_underflow_count(); }", 3},
		// FREE: a map freed at exit releases its keys/vals/used arrays + the box
		// ($__fern_map_release). Value-correct + detector clean.
		{"map-free-i32", "function main(): i32 { var m = map_new_i32(8); m = m.insert(3, 40); return m.get_or(3, -1) + 2 + __fern_rc_underflow_count(); }", 42},
		{"map-free-str", "function main(): i32 { var m = map_new(\"x\"); m = m.insert(\"a\", 40); return m.get_or(\"a\", -1) + 2 + __fern_rc_underflow_count(); }", 42},
		// FREE churn: 200k growing maps built + dropped. The box + 3 arrays are
		// reclaimed each cycle (no OOM), detector clean — proves real free, not
		// just counting (200k unreclaimed maps would exhaust memory).
		{"map-free-churn-clean", "function mk(): i32 { var m = map_new_i32(2); var k = 0; while (k < 30) { m = m.insert(k, k); k = k + 1; } return m.get_or(25, -1); } function main(): i32 { var n = 0; var k = 0; while (k < 200000) { n = mk(); k = k + 1; } return (n % 100) + __fern_rc_underflow_count(); }", 25},
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
