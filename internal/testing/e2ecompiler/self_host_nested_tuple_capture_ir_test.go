package e2ecompiler

import (
	"testing"
)

var nestedTupleCaptureIRCases = []struct {
	name string
	main string
}{
	{"nested-2level", `struct H { f: (i32) => i32, id: i32 }
function g(): i32 {
	let t: ((i32, i32), i32) = ((3, 4), 5);
	let (p, c) = t;
	let (a, b) = p;
	let h: H = H { f: (x: i32): i32 => { return x + a + b + c; }, id: a };
	return h.f(1) + h.id;
}
function main(): i32 { return g(); }`},
	{"direct-tuple-capture", `struct H { f: (i32) => i32, id: i32 }
function g(): i32 {
	let t: ((i32, i32), i32) = ((3, 4), 5);
	let (p, c) = t;
	let h: H = H { f: (x: i32): i32 => { return x + p.0 + p.1 + c; }, id: c };
	return h.f(1) + h.id;
}
function main(): i32 { return g(); }`},
	{"nested-3level", `struct H { f: (i32) => i32, id: i32 }
function g(): i32 {
	let t: (((i32, i32), i32), i32) = (((1, 2), 3), 4);
	let (q, d) = t;
	let (p, c) = q;
	let (a, b) = p;
	let h: H = H { f: (x: i32): i32 => { return x + a + b + c + d; }, id: a };
	return h.f(1) + h.id;
}
function main(): i32 { return g(); }`},
}

func TestSelfHostNestedTupleCaptureIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range nestedTupleCaptureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			if stderr, code := cli.exitOf(t, string(src), "x86-64-linux"); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)\n%s", tc.name, code, want, stderr)
			}
		})
	}
}
