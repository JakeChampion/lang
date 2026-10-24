package e2ecompiler

import "testing"

var callOnCallIRCases = []struct {
	name string
	main string
}{
	{"nocap", `function mk(): (i32) => i32 { return (b: i32): i32 => { return b + 1; }; }
function main(): i32 { return mk()(4); }`},
	{"in-expr", `function mk(): (i32) => i32 { return (b: i32): i32 => { return b * 3; }; }
function main(): i32 { return mk()(5) + 1; }`},
	{"two-arg", `function mk(): (i32, i32) => i32 { return (a: i32, b: i32): i32 => { return a + b; }; }
function main(): i32 { return mk()(5, 6); }`},
	{"twice", `function mk(): (i32) => i32 { return (b: i32): i32 => { return b + 1; }; }
function main(): i32 { return mk()(4) + mk()(10); }`},
	{"bind-regress", `function mk(): (i32) => i32 { return (b: i32): i32 => { return b + 1; }; }
function main(): i32 { let g = mk(); return g(4); }`},
	{"fnarr-regress", `function inc(b: i32): i32 { return b + 1; }
function main(): i32 { let fs: ((i32) => i32)[] = [inc]; return fs[0](4); }`},
}

func TestSelfHostCallOnCallIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range callOnCallIRCases {
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
