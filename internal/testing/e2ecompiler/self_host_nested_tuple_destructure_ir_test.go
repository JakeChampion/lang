package e2ecompiler

import (
	"testing"
)

var nestedTupleDestructureIRCases = []struct {
	name string
	main string
}{
	{"nested-str-2level", `function g(): i32 {
	let t: ((string, i32), i32) = (("hi", 4), 5);
	let (p, c) = t;
	let (s, b) = p;
	return s.len() + b + c;
}
function main(): i32 { return g(); }`},
	{"nested-str-dotN", `function g(): i32 {
	let t: ((string, i32), i32) = (("hey", 4), 5);
	let (p, c) = t;
	return p.0.len() + p.1 + c;
}
function main(): i32 { return g(); }`},
	{"nested-str-3level", `function g(): i32 {
	let t: (((string, i32), i32), i32) = ((("ab", 1), 2), 3);
	let (q, d) = t;
	let (p, c) = q;
	let (s, b) = p;
	return s.len() + b + c + d;
}
function main(): i32 { return g(); }`},
	{"nested-i32-2level", `function g(): i32 {
	let t: ((i32, i32), i32) = ((3, 4), 5);
	let (p, c) = t;
	let (a, b) = p;
	return a + b + c;
}
function main(): i32 { return g(); }`},
}

func TestSelfHostNestedTupleDestructureIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range nestedTupleDestructureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			if stderr, code := cli.exitOf(t, string(src), "x86-64-linux"); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)\n%s", tc.name, code, want, stderr)
			}
		})
	}
}
