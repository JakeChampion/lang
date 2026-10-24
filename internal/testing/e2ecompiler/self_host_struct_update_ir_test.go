package e2ecompiler

import "testing"

var structUpdateIRCases = []struct {
	name string
	main string
}{
	{"single-override", `struct P { x: i32, y: i32, z: i32 }
function main(): i32 { let p: P = P { x: 1, y: 2, z: 3 }; let q: P = P { ...p, y: 20 }; return q.x + q.y + q.z; }`},
	{"string-field-copy", `struct S { name: string, n: i32 }
function main(): i32 { let s: S = S { name: "hi", n: 3 }; let t: S = S { ...s, n: 9 }; if (t.name == "hi") { return t.n; } return 0; }`},
	{"update-in-return", `struct P { a: i32, b: i32 }
function bump(p: P): P { return P { ...p, b: p.b + 100 }; }
function main(): i32 { let p: P = P { a: 5, b: 6 }; let q: P = bump(p); return q.a + q.b; }`},
	{"functional-loop", `struct C { n: i32 }
function inc(c: C): C { return C { ...c, n: c.n + 1 }; }
function main(): i32 { let c: C = C { n: 0 }; let i: i32 = 0; while (i < 5) { c = inc(c); i = i + 1; } return c.n; }`},
}

func TestSelfHostStructUpdateIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range structUpdateIRCases {
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
