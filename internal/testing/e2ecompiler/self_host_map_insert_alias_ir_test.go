package e2ecompiler

import "testing"

// mapInsertAliasIRCases exercise the self-reassign `m = m.insert(k, v)` when
// the map `m` has a lasting LOCAL alias (#3633 — the
// map sibling of the array `.with` fix #3599).
//
// An insert that mutates the map's storage in place is unsound once `m` is aliased (`let n = m`): the in-place write mutates
// the buffer `n` still references, so `n` observes the change. An aliased
// self-reassign therefore inserts into a sole-owned clone and leaves `n`
// unchanged, as the interpreter's copy-on-write does. The unaliased "no-alias"
// case still takes the in-place path.
//
// Programs build maps with the `Map {}` literal like the other self-host map IR
// tests; `want` values are verified against the interpreter.
var mapInsertAliasIRCases = []struct {
	name string
	main string
	want int
}{
	// The minimal repro: overwrite an existing key while an alias is live. The
	// in-place mutation made n[1]==99 too (99+99=198); copy-on-write keeps n[1]==10.
	{"overwrite", `let m: Map[i32, i32] = Map {}; m = m.insert(1, 10); let n = m; m = m.insert(1, 99); return m.get_or(1, 0) + n.get_or(1, 0);`, 109},
	// Insert a NEW key while an alias is live: n must not gain key 2.
	{"new-key", `let m: Map[i32, i32] = Map {}; m = m.insert(1, 10); let n = m; m = m.insert(2, 20); return m.get_or(2, 0) + n.get_or(2, 0);`, 20},
	// String-keyed map: the clone copies string-pointer key/value slots correctly.
	{"string-key", `let m: Map[string, i32] = Map {}; m = m.insert("a", 5); let n = m; m = m.insert("a", 9); return m.get_or("a", 0) + n.get_or("a", 0);`, 14},
	// REGRESSION: no alias — the in-place fast path must still apply and be correct.
	{"no-alias", `let m: Map[i32, i32] = Map {}; m = m.insert(1, 10); m = m.insert(1, 99); return m.get_or(1, 0);`, 99},
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
