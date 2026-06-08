package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcStructBoxWasm proves the Phase-1e container-layout
// foundation for structs (and enum variants, which share the struct
// layout): a struct block is now rc-boxed via the generic
// $__fern_str_box (8-byte rc+bsz header, returns base+8), so it carries
// an rc word at [s-8] while every s-relative access is unchanged — the
// type id stays at slot 0 (so `match` reads the right tag) and each field
// stays at struct_field_off. Observed through __fern_rc_is_unique: a fresh
// struct / variant value is unique (rc==1). Field values + array/string
// members (already construction-inc'd) survive. Counting + recursive
// field-release ride on this foundation in later slices.
//
// Extern-ABI structs (canonical-ABI result records) are intentionally left
// raw in this slice — layout-only never sweeps structs, so the mix is
// value-safe; they migrate when struct counting lands.
func TestSelfHostRcStructBoxWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm struct-box e2e")
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
		// A fresh struct literal is rc-boxed at rc 1 => unique.
		{"struct-fresh-unique", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 5, y: 7 }; return __fern_rc_is_unique(p); }", 1},
		// Field values survive the rc header (s-relative access unchanged).
		{"struct-values-intact", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 30, y: 12 }; return p.x + p.y; }", 42},
		// A struct holding an array field: value intact, detector clean.
		{"struct-holds-array", "struct B { xs: i32[], n: i32 } function main(): i32 { var ys: i32[] = [1, 2, 3]; var b = B { xs: ys, n: 9 }; return b.xs[2] + b.n + __fern_rc_underflow_count(); }", 12},
		// A struct holding a string field: value intact, detector clean.
		{"struct-holds-string", "struct N { name: string, n: i32 } function main(): i32 { var s: string = \"ab\" + \"cd\"; var v = N { name: s, n: 5 }; return v.name.len() + v.n + __fern_rc_underflow_count(); }", 9},
		// Struct update syntax (`S { ...base, f: v }`) keeps both copied and
		// overridden fields correct under the rc header.
		{"struct-update-intact", "struct P { x: i32, y: i32 } function main(): i32 { var a = P { x: 10, y: 20 }; var b = P { ...a, y: 32 }; return b.x + b.y; }", 42},
		// A unit enum variant (0-field struct) is boxed too and matches.
		{"unit-variant-match", "enum E { A, B } function main(): i32 { var e: E = B; match (e) { A => { return 1; }, B => { return 41; } } }", 41},
		// A positional variant constructor carries its payload at field 0 and
		// is unique when fresh.
		{"variant-payload-intact", "enum Shape { Circle(i32), Square(i32) } function area(s: Shape): i32 { match (s) { Circle(r) => { return r * r; }, Square(w) => { return w + w; } } } function main(): i32 { var c: Shape = Circle(6); return area(c); }", 36},
		// A built struct returned survives in the caller (no struct sweep yet,
		// so this is a value-correctness + detector-clean check across the
		// rc-boxed return path).
		{"struct-return-intact", "struct P { x: i32, y: i32 } function mk(): P { return P { x: 8, y: 34 }; } function main(): i32 { var p = mk(); return p.x + p.y + __fern_rc_underflow_count(); }", 42},
		// A struct built each loop iteration: detector stays clean (layout +
		// construction-incs only; free off, so this leaks soundly).
		{"struct-loop-clean", "struct P { x: i32, y: i32 } function main(): i32 { var s = 0; var k = 0; while (k < 1000) { var p = P { x: k, y: 2 }; s = s + p.y; k = k + 1; } return (s % 7) + __fern_rc_underflow_count(); }", 5},
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
