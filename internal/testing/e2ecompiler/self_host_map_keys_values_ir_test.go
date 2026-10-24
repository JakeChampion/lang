package e2ecompiler

import "testing"

var mapKeysValuesIRCases = []struct {
	name string
	main string
}{
	{"keys-i32", `import "core/map"; function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(10, 1); m = m.insert(20, 1); let ks = m.keys(); let s: i32 = 0; for k in ks { s = s + k; } return s; }`},
	{"vals-i32", `import "core/map"; function main(): i32 { let m: Map[string, i32] = map_new(8); m = m.insert("a", 4); m = m.insert("b", 6); let vs = m.values(); let s: i32 = 0; for v in vs { s = s + v; } return s; }`},
	{"keys-str", `import "core/map"; function main(): i32 { let m: Map[string, i32] = map_new(8); m = m.insert("ab", 1); m = m.insert("c", 1); let ks = m.keys(); let s: i32 = 0; for k in ks { s = s + k.len(); } return s; }`},
	{"vals-str", `import "core/map"; function main(): i32 { let m: Map[i32, string] = map_new(8); m = m.insert(1, "ab"); m = m.insert(2, "c"); let vs = m.values(); let s: i32 = 0; for v in vs { s = s + v.len(); } return s; }`},
	{"keys-len", `import "core/map"; function main(): i32 { let m: Map[i32, i32] = map_new(8); m = m.insert(5, 1); m = m.insert(6, 1); let ks = m.keys(); return ks.len(); }`},
	{"annot-i32", `import "core/map"; function main(): i32 { let m: Map[string, i32] = map_new(8); m = m.insert("a", 42); let vs: i32[] = m.values(); let s: i32 = 0; for v in vs { s = s + v; } return s; }`},
}

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
