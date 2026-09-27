package e2eselfhost

import "testing"

// mapKeysValuesIRCases pin an UNANNOTATED `var x = m.keys()` / `m.values()`
// binding consumed by a `for … in x` loop (or `.len()`) to the self-host IR path
// on x86-64 + wasm. The annotated form (`var x: i32[] = m.keys()`) already routed
// IR — the array-local foreach machinery is complete — but the unannotated var
// binding never marked the slot's array-ness / element type from the map's K/V,
// so it bailed the whole module to the legacy AST emitter. #2691 teaches the
// unannotated-var array inference (and the expr_is_arr_src / expr_is_strarr
// predicates) to recognize map `.keys()`/`.values()` via one map_kv_elem_tag
// gate. Scope: i32/string keys and i32/string values — the 8-byte i64/f64 value
// cases hit a SEPARATE pre-existing op_map_values wasm bug (the annotated form
// mis-sums on wasm too) and stay on AST. Each case is oracle-checked against the
// interpreter; all need `import "core/map";`.
var mapKeysValuesIRCases = []struct {
	name string
	main string
}{
	// i32-keyed map: sum the keys. 10 + 20 = 30.
	{"keys-i32", `import "core/map"; function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(10, 1); m = m.insert(20, 1); var ks = m.keys(); var s: i32 = 0; for k in ks { s = s + k; } return s; }`},
	// i32 values: sum them. 4 + 6 = 10.
	{"vals-i32", `import "core/map"; function main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert("a", 4); m = m.insert("b", 6); var vs = m.values(); var s: i32 = 0; for v in vs { s = s + v; } return s; }`},
	// string keys: sum their lengths. len("ab")+len("c") = 3.
	{"keys-str", `import "core/map"; function main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert("ab", 1); m = m.insert("c", 1); var ks = m.keys(); var s: i32 = 0; for k in ks { s = s + k.len(); } return s; }`},
	// string values: sum their lengths. len("ab")+len("c") = 3.
	{"vals-str", `import "core/map"; function main(): i32 { var m: Map[i32, string] = map_new(8); m = m.insert(1, "ab"); m = m.insert(2, "c"); var vs = m.values(); var s: i32 = 0; for v in vs { s = s + v.len(); } return s; }`},
	// keys() result used by .len() (no foreach). 2 entries.
	{"keys-len", `import "core/map"; function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(5, 1); m = m.insert(6, 1); var ks = m.keys(); return ks.len(); }`},
	// Regression: the ANNOTATED form was already on the IR path. 42.
	{"annot-i32", `import "core/map"; function main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert("a", 42); var vs: i32[] = m.values(); var s: i32 = 0; for v in vs { s = s + v; } return s; }`},
}

// TestSelfHostMapKeysValuesIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostMapKeysValuesIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range mapKeysValuesIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
