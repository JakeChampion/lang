package e2ecompiler

import "testing"

var constStringIRCases = []struct {
	name string
	main string
}{
	{"const-str-len", `const NAME: string = "hi"; function main(): i32 { return NAME.len(); }`},
	{"const-str-len3", `const NAME: string = "abc"; function main(): i32 { return NAME.len(); }`},
	{"const-str-concat", `const NAME: string = "hi"; function main(): i32 { return (NAME + "xx").len(); }`},
	{"const-i32-ref", `const NAME: i32 = 8; function main(): i32 { return NAME; }`},
	{"local-str-len", `function main(): i32 { let s: string = "hi"; return s.len(); }`},
	{"const-str-escapes", `const NAME: string = "a\nb\"c"; function main(): i32 { return NAME.len(); }`},
}

func TestSelfHostConstStringIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range constStringIRCases {
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
