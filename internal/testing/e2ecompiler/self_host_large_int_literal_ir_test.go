package e2ecompiler

import "testing"

var largeIntLiteralIRCases = []struct {
	name string
	src  string
}{
	{"u64-literal-mod", `function main(): i32 {
    let big: u64 = 9000000000000000123 as u64;
    return (big % 1000 as u64) as i32;
}`},
	{"i64-literal-roundtrip", `function main(): i32 {
    let n: i64 = 5000000000000000457 as i64;
    if (n == 5000000000000000457 as i64) { return 77; }
    return 0;
}`},
	{"u64-literal-compare", `function main(): i32 {
    let big: u64 = 9000000000000000000 as u64;
    if (big > 1 as u64) { return 7; }
    return 0;
}`},
}

func TestSelfHostLargeIntLiteralIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range largeIntLiteralIRCases {
		src := tc.src
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
