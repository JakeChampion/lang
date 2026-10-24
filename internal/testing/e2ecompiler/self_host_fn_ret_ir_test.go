package e2ecompiler

import "testing"

var fnRetIRCases = []struct {
	name string
	main string
}{
	{"nocap", `function mk(): (i32) => i32 { return (b: i32): i32 => { return b + 1; }; }
function main(): i32 { let g = mk(); return g(4); }`},
	{"nocap-mul", `function mk(): (i32) => i32 { return (b: i32): i32 => { return b * 3; }; }
function main(): i32 { let g = mk(); return g(4); }`},
	{"nocap-twice", `function mk(): (i32) => i32 { return (b: i32): i32 => { return b + 1; }; }
function main(): i32 { let g = mk(); return g(4) + g(10); }`},
	{"nocap-2arg", `function mk(): (i32, i32) => i32 { return (a: i32, b: i32): i32 => { return a + b; }; }
function main(): i32 { let g = mk(); return g(5, 6); }`},
	{"cap-regress", `function mk(n: i32): (i32) => i32 { return (b: i32): i32 => { return b + n; }; }
function main(): i32 { let g = mk(10); return g(4); }`},
	{"named-regress", `function inc(b: i32): i32 { return b + 1; }
function mk(): (i32) => i32 { return inc; }
function main(): i32 { let g = mk(); return g(4); }`},
}

func TestSelfHostFnRetIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range fnRetIRCases {
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
