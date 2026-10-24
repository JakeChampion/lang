package e2ecompiler

import "testing"

var labeledBreakIRCases = []struct {
	name string
	main string
}{
	{"break-outer", `function main(): i32 { let i = 0; let c = 0; outer: while (i < 5) { i = i + 1; let j = 0; while (j < 5) { j = j + 1; c = c + 1; if (j == 2) { break outer; } } } return c; }`},
	{"continue-outer", `function main(): i32 { let i = 0; let c = 0; outer: while (i < 3) { i = i + 1; let j = 0; while (j < 3) { j = j + 1; if (j == 1) { continue outer; } c = c + 1; } } return c; }`},
	{"labeled-for-break", `function main(): i32 { let c = 0; outer: for i in 0..3 { for j in 0..3 { c = c + 1; if (j == 1) { break outer; } } } return c; }`},
	{"labeled-c-for-break", `function main(): i32 { let c = 0; outer: for (let i = 0; i < 3; i = i + 1) { for j in 0..3 { c = c + 1; if (j == 1) { break outer; } } } return c; }`},
	{"labeled-c-for-continue", `function main(): i32 { let c = 0; outer: for (let i = 0; i < 3; i = i + 1) { for j in 0..3 { c = c + 1; if (j == 0) { continue outer; } } } return c; }`},
	{"triple-continue-mid", `function main(): i32 { let c = 0; mid: for i in 0..3 { for j in 0..3 { for k in 0..3 { c = c + 1; if (k == 0) { continue mid; } } } } return c; }`},
	{"break-from-match", `function main(): i32 { let c = 0; outer: for i in 0..4 { for j in 0..4 { match (j) { 2 => { break outer; }, _ => { c = c + 1; } } } } return c; }`},
	{"plain-break", `function main(): i32 { let i = 0; let c = 0; while (i < 3) { i = i + 1; let j = 0; while (j < 3) { j = j + 1; c = c + 1; if (j == 1) { break; } } } return c; }`},
	{"plain-continue", `function main(): i32 { let i = 0; let s = 0; while (i < 5) { i = i + 1; if (i == 3) { continue; } s = s + i; } return s; }`},
}

func TestSelfHostLabeledBreakIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range labeledBreakIRCases {
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
