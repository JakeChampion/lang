package e2ecompiler

import "testing"

const mapValuePtrIRPrelude = `function ap(m: Map[string, string[]], k: string, v: string): Map[string, string[]] {
    match (m.get(k)) {
        Some(e) => { return m.insert(k, e.append(v)); },
        None => { let a: string[] = [v]; return m.insert(k, a); },
    }
    return m;
}
function vcount(m: Map[string, string[]], k: string): i32 {
    match (m.get(k)) { Some(v) => { return v.len(); }, None => { return 0; }, }
    return 0;
}
function iput(m: Map[string, i32], k: string, v: i32): Map[string, i32] { return m.insert(k, v); }
function iget(m: Map[string, i32], k: string): i32 {
    match (m.get(k)) { Some(v) => { return v; }, None => { return 0; }, }
    return 0;
}
`

var mapValuePtrIRCases = []struct {
	name string
	main string
	want int
}{
	{"dup-key-sibling", `let m: Map[string, string[]] = Map {}; m = ap(m, "a", "1"); m = ap(m, "b", "2"); m = ap(m, "a", "3"); return vcount(m, "a") * 10 + vcount(m, "b");`, 21},
	{"three-keys", `let m: Map[string, string[]] = Map {}; m = ap(m, "x", "1"); m = ap(m, "y", "2"); m = ap(m, "z", "3"); return vcount(m, "x") * 100 + vcount(m, "y") * 10 + vcount(m, "z");`, 111},
	{"scalar-values", `let m: Map[string, i32] = Map {}; m = iput(m, "a", 5); m = iput(m, "b", 7); return iget(m, "a") + iget(m, "b");`, 12},
}

func mapValuePtrIRSrc(mainBody string) string {
	return "import \"core/map\";\n" + mapValuePtrIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostMapValuePtrIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range mapValuePtrIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, mapValuePtrIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
