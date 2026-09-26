package e2eselfhost

import "testing"

// mapInsertAliasIRCases exercise the self-reassign `m = m.insert(k, v)` through
// the self-host IR path when the map `m` has a lasting LOCAL alias (#3633 — the
// map sibling of the array `.with` fix #3599).
//
// The builtin op_map_set mutates the map's parallel keys[]/values[] in place,
// which is unsound once `m` is aliased (`var n = m`): the in-place write mutates
// the buffer `n` still references, so `n` observes the change. The interpreter
// and the native (Perceus) backend both copy-on-write and leave `n` unchanged.
// The fix detects the alias at lower_func time (aliased_array_names_of, shared
// with #3599) and routes the aliased self-reassign through a map clone
// (lower_map_clone_insert: fresh map_new + a copy loop over keys()/values(),
// mutate the sole-owned clone) instead of the in-place store. The unaliased
// "no-alias" case still takes the in-place path.
//
// Programs build maps with the `Map {}` literal like the other self-host map IR
// tests; `want` values are verified against the native x86-64 backend and the
// interpreter.
var mapInsertAliasIRCases = []struct {
	name string
	main string
	want int
}{
	// The minimal repro: overwrite an existing key while an alias is live. The
	// in-place mutation made n[1]==99 too (99+99=198); copy-on-write keeps n[1]==10.
	{"overwrite", `var m: Map[i32, i32] = Map {}; m = m.insert(1, 10); var n = m; m = m.insert(1, 99); return m.get_or(1, 0) + n.get_or(1, 0);`, 109},
	// Insert a NEW key while an alias is live: n must not gain key 2.
	{"new-key", `var m: Map[i32, i32] = Map {}; m = m.insert(1, 10); var n = m; m = m.insert(2, 20); return m.get_or(2, 0) + n.get_or(2, 0);`, 20},
	// String-keyed map: the clone copies string-pointer key/value slots correctly.
	{"string-key", `var m: Map[string, i32] = Map {}; m = m.insert("a", 5); var n = m; m = m.insert("a", 9); return m.get_or("a", 0) + n.get_or("a", 0);`, 14},
	// REGRESSION: no alias — the in-place fast path must still apply and be correct.
	{"no-alias", `var m: Map[i32, i32] = Map {}; m = m.insert(1, 10); m = m.insert(1, 99); return m.get_or(1, 0);`, 99},
}

func mapInsertAliasIRSrc(mainBody string) string {
	return "import \"core/map\";\n" + "function main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostMapInsertAliasIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostMapInsertAliasIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range mapInsertAliasIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, mapInsertAliasIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
