package e2ecompiler

import "testing"

var nestedTupleIRCases = []struct {
	name string
	main string
}{
	{"right-nest", `function main(): i32 { let t: (i32, (i32, i32)) = (1, (2, 3)); return t.1.1; }`},
	{"sum-across", `function main(): i32 { let t: (i32, (i32, i32)) = (1, (2, 3)); return t.0 + t.1.0 + t.1.1; }`},
	{"left-nest", `function main(): i32 { let t: ((i32, i32), i32) = ((4, 5), 6); return t.0.0 + t.0.1 + t.1; }`},
	{"triple-nest", `function main(): i32 { let t: (i32, (i32, (i32, i32))) = (1, (2, (3, 4))); return t.1.1.1; }`},
	{"nest-with-string", `function main(): i32 { let t: (i32, (string, i32)) = (1, ("ab", 9)); return t.1.0.len() + t.1.1; }`},
	{"nest-then-i64", `function main(): i32 { let t: ((i32, i32), i64) = ((1, 2), 5000000000); return t.0.0 + t.0.1 + (t.1 / 1000000000) as i32; }`},
}

func TestSelfHostNestedTupleIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range nestedTupleIRCases {
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
