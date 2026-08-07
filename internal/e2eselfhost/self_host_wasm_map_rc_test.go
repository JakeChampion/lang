package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcMapGrowWasm proves the map keys/vals/used arrays are now
// rc-boxed (via $__fern_str_box — flat, so [arr + i*4] addressing is
// unchanged) and that $__fern_map_grow FREES the old arrays after rehashing
// instead of leaking them on every resize: a map that grows N times otherwise
// leaks 3*N raw arrays. Values stay correct (the string key/value pointers
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
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// String-keyed map grown from cap 2 by 5 distinct keys (forces a
		// grow+rehash): values intact + detector clean.
		{"map-grow-str-keys", "function main(): i32 { var m = map_new(2); m = m.insert(\"a\", 10); m = m.insert(\"b\", 20); m = m.insert(\"c\", 30); m = m.insert(\"d\", 13); m = m.insert(\"e\", 21); return m.get_or(\"c\", -1) + m.get_or(\"d\", -1) + __rc_underflow_count(); }", 43},
		// i32-keyed map grown by 100 distinct keys (several resizes): a probed
		// lookup is value-correct + detector clean.
		{"map-grow-i32-keys", "function main(): i32 { var m = map_new_i32(2); var k = 0; while (k < 100) { m = m.insert(k, k); k = k + 1; } return m.get_or(50, -1) + m.get_or(70, -1) + __rc_underflow_count(); }", 120},
		// A churn: build a growing map each iteration and drop it. The grow path
		// reclaims old arrays so memory doesn't blow up; detector clean.
		{"map-grow-churn", "function mk(): i32 { var m = map_new_i32(2); var k = 0; while (k < 30) { m = m.insert(k, k); k = k + 1; } return m.get_or(25, -1); } function main(): i32 { var n = 0; var k = 0; while (k < 5000) { n = mk(); k = k + 1; } return (n % 100) + __rc_underflow_count(); }", 25},
		// String values that survive the array reallocation on grow.
		{"map-grow-str-vals", "function main(): i32 { var m = map_new(2); m = m.insert(\"x\", 1); m = m.insert(\"y\", 2); m = m.insert(\"z\", 3); m = m.insert(\"w\", 4); return m.get_or(\"x\", -1) + m.get_or(\"w\", -1) + m.len() + __rc_underflow_count(); }", 9},
		// COUNTING milestone (free off): an owned map local is released (rc dec)
		// at exit, value-correct + detector clean.
		{"map-swept-clean", "function main(): i32 { var m = map_new_i32(8); m = m.insert(3, 40); return m.get_or(3, -1) + 2 + __rc_underflow_count(); }", 42},
		// A string-keyed map swept at exit (the box dec'd; arrays + strings leak
		// soundly while free is off) — value-correct + detector clean.
		{"map-str-swept-clean", "function main(): i32 { var m = map_new(8); m = m.insert(\"k\", 40); return m.get_or(\"k\", -1) + 2 + __rc_underflow_count(); }", 42},
		// A map re-built each loop iteration: detector stays clean (counting;
		// free off, so intermediates + the grow-freed arrays don't over-release).
		{"map-counting-churn-clean", "function mk(): i32 { var m = map_new(2); m = m.insert(\"a\", 1); m = m.insert(\"b\", 2); m = m.insert(\"c\", 3); m = m.insert(\"d\", 4); return m.get_or(\"c\", -1); } function main(): i32 { var k = 0; var s = 0; while (k < 5000) { s = mk(); k = k + 1; } return (s % 100) + __rc_underflow_count(); }", 3},
		// FREE: a map freed at exit releases its keys/vals/used arrays + the box
		// ($__fern_map_release). Value-correct + detector clean.
		{"map-free-i32", "function main(): i32 { var m = map_new_i32(8); m = m.insert(3, 40); return m.get_or(3, -1) + 2 + __rc_underflow_count(); }", 42},
		{"map-free-str", "function main(): i32 { var m = map_new(\"x\"); m = m.insert(\"a\", 40); return m.get_or(\"a\", -1) + 2 + __rc_underflow_count(); }", 42},
		// FREE churn: 200k growing maps built + dropped. The box + 3 arrays are
		// reclaimed each cycle (no OOM), detector clean — proves real free, not
		// just counting (200k unreclaimed maps would exhaust memory).
		{"map-free-churn-clean", "function mk(): i32 { var m = map_new_i32(2); var k = 0; while (k < 30) { m = m.insert(k, k); k = k + 1; } return m.get_or(25, -1); } function main(): i32 { var n = 0; var k = 0; while (k < 200000) { n = mk(); k = k + 1; } return (n % 100) + __rc_underflow_count(); }", 25},
		// String KEY release: a heap string key (construction-inc'd on insert) is
		// freed on the map's death (map_release releases occupied-slot keys when
		// kis==1). Value-correct + detector clean.
		{"map-heap-key-released", "function main(): i32 { var m = map_new(8); var k: string = \"ab\" + \"cd\"; m = m.insert(k, 5); return m.get_or(k, -1) + 37 + __rc_underflow_count(); }", 42},
		// A string LITERAL key is immortal — the inc/dec are guard no-ops, value
		// still correct.
		{"map-literal-key-clean", "function main(): i32 { var m = map_new(8); m = m.insert(\"x\", 40); return m.get_or(\"x\", -1) + 2 + __rc_underflow_count(); }", 42},
		// Churn: 50k maps with HEAP string keys built + freed; the keys reclaim
		// each cycle (no OOM), detector clean — the wordcount-style leak closed.
		{"map-heap-key-churn", "function mk(): i32 { var m = map_new(2); var a: string = \"k\" + \"1\"; var b: string = \"k\" + \"2\"; m = m.insert(a, 3); m = m.insert(b, 4); return m.get_or(a, -1) + m.get_or(b, -1); } function main(): i32 { var k = 0; var s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 100) + __rc_underflow_count(); }", 7},
		// String VALUE release: a heap string value (construction-inc'd on insert
		// when vis==1) is freed on the map's death (map_release releases each
		// occupied slot's value). i32 keys (kis 0), string values (vis 1).
		// Value-correct + detector clean.
		{"map-str-value-released", "function main(): i32 { var m = map_new_i32(8); var v: string = \"ab\" + \"cd\"; m = m.insert(1, v); return m.get_or(1, \"\").len() + 38 + __rc_underflow_count(); }", 42},
		// A string LITERAL value is immortal — the inc/dec guard no-ops, value
		// still correct.
		{"map-str-value-literal-clean", "function main(): i32 { var m = map_new_i32(8); m = m.insert(5, \"hello\"); return m.get_or(5, \"\").len() + 37 + __rc_underflow_count(); }", 42},
		// Overwrite: re-inserting the same key with a NEW string value releases
		// the OLD value + construction-inc's the new (balanced, no leak / no
		// over-release). Value reads the latest; detector clean.
		{"map-str-value-overwrite", "function main(): i32 { var m = map_new_i32(8); var a: string = \"x\" + \"x\"; var b: string = \"yy\" + \"zz\"; m = m.insert(7, a); m = m.insert(7, b); return m.get_or(7, \"\").len() + 38 + __rc_underflow_count(); }", 42},
		// Both string KEY and string VALUE released (kis 1, vis 1) on the map's
		// death — value-correct + detector clean.
		{"map-str-key-and-value", "function main(): i32 { var m = map_new(8); var v: string = \"ab\" + \"cd\"; m = m.insert(\"key\", v); return m.get_or(\"key\", \"\").len() + 38 + __rc_underflow_count(); }", 42},
		// Churn: 50k maps with HEAP string values built + freed; the values
		// reclaim each cycle (no OOM), detector clean — the string-value leak
		// closed (50k leaked value strings would exhaust memory).
		{"map-str-value-churn", "function mk(): i32 { var m = map_new_i32(2); var a: string = \"k\" + \"1\"; var b: string = \"k\" + \"2\"; m = m.insert(1, a); m = m.insert(2, b); return m.get_or(1, \"\").len() + m.get_or(2, \"\").len(); } function main(): i32 { var k = 0; var s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 4},
		// ARRAY value (i32[]): value-correct via index read + the array buffer is
		// construction-inc'd on insert and reclaimed (arr_dec) on the map's death.
		{"map-arrval-get", "function main(): i32 { var m = map_new_i32(8); m = m.insert(1, [40, 2]); var xs = m.get_or(1, [0, 0]); return xs[0] + xs[1] + __rc_underflow_count(); }", 42},
		// Churn: 200k i32[]-valued maps built + freed; the array buffers reclaim
		// each cycle (no OOM), detector clean — complete for scalar-element arrays.
		{"map-arrval-churn", "function mk(): i32 { var m = map_new_i32(2); m = m.insert(1, [1, 2, 3, 4]); m = m.insert(2, [5, 6, 7, 8]); var xs = m.get_or(1, [0]); return xs[3]; } function main(): i32 { var k = 0; var s = 0; while (k < 200000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 4},
		// String key + i32[] value: the key AND the array buffer both reclaim each
		// cycle across 200k, no OOM, detector clean.
		{"map-strkey-arrval-churn", "function mk(): i32 { var m = map_new(2); var a: string = \"k\" + \"1\"; m = m.insert(a, [9, 8, 7]); var xs = m.get_or(a, [0]); return xs[0]; } function main(): i32 { var k = 0; var s = 0; while (k < 200000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 2},
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
