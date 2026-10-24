package e2ecompiler

import "testing"

var stringEscapeIRCases = []struct {
	name string
	main string
}{
	{"newline-tab-len", `function main(): i32 { return "a\tb\nc".len(); }`},
	{"backslash-quote", `function main(): i32 { return "x\\y\"z".len(); }`},
	{"hex-escape-A", `function main(): i32 { return ("\x41")[0] as i32; }`},
	{"hex-escape-z", `function main(): i32 { return ("\x7a")[0] as i32; }`},
	{"cr-null-len", `function main(): i32 { return "\r\0".len(); }`},
	{"null-hex-len", `function main(): i32 { return "\x00".len(); }`},
	{"escape-byte-index", `function main(): i32 { let s: string = "a\nb"; return s[1] as i32; }`},
	{"concat-escapes", `function main(): i32 { return ("\t" + "\n").len(); }`},
	{"mixed-escapes-len", `function main(): i32 { return "a\tb\\c\"d\ne".len(); }`},
}

func TestSelfHostStringEscapesIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range stringEscapeIRCases {
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
