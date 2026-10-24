package e2ecompiler

import "testing"

var tupleDestructureNestedStrIRCases = []struct {
	name string
	main string
}{
	{"nested-str", `function g(): i32 {
    let t: ((string, i32), i32) = (("hi", 4), 5);
    let (p, c) = t;
    let (s, b) = p;
    return s.len() + b + c;
}
function main(): i32 { return g(); }`},
	{"nested-str-dotn", `function g(): i32 {
    let t: ((string, i32), i32) = (("hi", 4), 5);
    let (p, c) = t;
    return p.0.len() + p.1 + c;
}
function main(): i32 { return g(); }`},
	{"nested-i32-regress", `function g(): i32 {
    let t: ((i32, i32), i32) = ((7, 4), 5);
    let (p, c) = t;
    let (a, b) = p;
    return a + b + c;
}
function main(): i32 { return g(); }`},
	{"fnfield-captured-destructure-str", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    let t: (string, i32) = ("hi", 4);
    let (s, b) = t;
    let h: H = H { f: (x: i32): string => { return s; }, id: b };
    return h.f(1).len() + h.id;
}
function main(): i32 { return g(); }`},
	{"fnfield-captured-str", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    let s: string = "hi";
    let h: H = H { f: (x: i32): string => { return s; }, id: 4 };
    return h.f(1).len() + h.id;
}
function main(): i32 { return g(); }`},
	{"fnfield-literal-str", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    let h: H = H { f: (x: i32): string => { return "hi"; }, id: 4 };
    return h.f(1).len() + h.id;
}
function main(): i32 { return g(); }`},
	{"fnfield-str-concat", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    let h: H = H { f: (x: i32): string => { return "hi"; }, id: 4 };
    return (h.f(1) + "!!").len() + h.id;
}
function main(): i32 { return g(); }`},
	{"fnfield-i32-regress", `struct H { f: (i32) => i32, id: i32 }
function g(): i32 {
    let n: i32 = 7;
    let h: H = H { f: (x: i32): i32 => { return n + x; }, id: 4 };
    return h.f(1) + h.id;
}
function main(): i32 { return g(); }`},
	{"fnfield-rebind-regress", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    let s: string = "hi";
    let h: H = H { f: (x: i32): string => { return s; }, id: 4 };
    let r: string = h.f(1);
    return r.len() + h.id;
}
function main(): i32 { return g(); }`},
}

func TestSelfHostTupleDestructureNestedStrIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range tupleDestructureNestedStrIRCases {
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
