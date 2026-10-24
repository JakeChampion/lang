package e2ecompiler

import "testing"

var mapInsertAliasIRCases = []struct {
	name string
	main string
	want int
}{
	{"overwrite", `let m: Map[i32, i32] = Map {}; m = m.insert(1, 10); let n = m; m = m.insert(1, 99); return m.get_or(1, 0) + n.get_or(1, 0);`, 109},
	{"new-key", `let m: Map[i32, i32] = Map {}; m = m.insert(1, 10); let n = m; m = m.insert(2, 20); return m.get_or(2, 0) + n.get_or(2, 0);`, 20},
	{"string-key", `let m: Map[string, i32] = Map {}; m = m.insert("a", 5); let n = m; m = m.insert("a", 9); return m.get_or("a", 0) + n.get_or("a", 0);`, 14},
	{"no-alias", `let m: Map[i32, i32] = Map {}; m = m.insert(1, 10); m = m.insert(1, 99); return m.get_or(1, 0);`, 99},
}

func mapInsertAliasIRSrc(mainBody string) string {
	return "import \"core/map\";\n" + "function main(): i32 { " + mainBody + " }\n"
}

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
