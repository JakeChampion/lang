package e2ecompiler

import "testing"

// tupleDestructureNestedStrIRCases pin the two #5306 shapes — string elements
// whose wrong typing on the IR path is a silent wrong answer, not a refusal
// (the interpreter is the oracle):
//
//  1. NESTED destructure: `let (p, c) = t; var (s, b) = p` over
//     ((string, i32), i32) must type the inner string element, or it reads
//     empty (11 → 9).
//
//  2. A struct fn-FIELD closure call chained into a string op:
//     `h.f(1).len()` where H.f: (i32) => string must know the call yields a
//     string, or it reads 0 (6 → 4) — for ANY captured or literal string
//     return, destructure or not. The `string` return is kept in
//     StructFieldDecl.fn_ret. (An annotated rebind `let r: string = h.f(1)`
//     takes the type from its annotation.)
//
// Each case is oracle-checked against the interpreter; results stay <= 120
// (the wasm exit-code clamp, #2908).
var tupleDestructureNestedStrIRCases = []struct {
	name string
	main string
}{
	// Gap 1: nested string destructure. 11 (was 9 — s.len() read 0).
	{"nested-str", `function g(): i32 {
    let t: ((string, i32), i32) = (("hi", 4), 5);
    let (p, c) = t;
    let (s, b) = p;
    return s.len() + b + c;
}
function main(): i32 { return g(); }`},
	// Gap 1 sibling: read the nested element via p.N instead of a second
	// destructure (the recorded element tags drive both). 11.
	{"nested-str-dotn", `function g(): i32 {
    let t: ((string, i32), i32) = (("hi", 4), 5);
    let (p, c) = t;
    return p.0.len() + p.1 + c;
}
function main(): i32 { return g(); }`},
	// Gap 1 regression: the all-i32 nested shape (#5201) still works. 16.
	{"nested-i32-regress", `function g(): i32 {
    let t: ((i32, i32), i32) = ((7, 4), 5);
    let (p, c) = t;
    let (a, b) = p;
    return a + b + c;
}
function main(): i32 { return g(); }`},
	// Gap 2: struct fn-field closure call chained into .len(), capturing a
	// destructure-bound string. 6 (was 4 — the .len() leg read 0).
	{"fnfield-captured-destructure-str", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    let t: (string, i32) = ("hi", 4);
    let (s, b) = t;
    let h: H = H { f: (x: i32): string => { return s; }, id: b };
    return h.f(1).len() + h.id;
}
function main(): i32 { return g(); }`},
	// Gap 2, plain-local capture (the same break, no destructure involved). 6.
	{"fnfield-captured-str", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    let s: string = "hi";
    let h: H = H { f: (x: i32): string => { return s; }, id: 4 };
    return h.f(1).len() + h.id;
}
function main(): i32 { return g(); }`},
	// Gap 2, no capture — a literal string return through the fn field. 6.
	{"fnfield-literal-str", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    let h: H = H { f: (x: i32): string => { return "hi"; }, id: 4 };
    return h.f(1).len() + h.id;
}
function main(): i32 { return g(); }`},
	// Gap 2 concat shape: the fn-field call result feeds `+` as a string. 8.
	{"fnfield-str-concat", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    let h: H = H { f: (x: i32): string => { return "hi"; }, id: 4 };
    return (h.f(1) + "!!").len() + h.id;
}
function main(): i32 { return g(); }`},
	// Regression: an i32-returning fn field is untouched by the fn_ret
	// threading. 12.
	{"fnfield-i32-regress", `struct H { f: (i32) => i32, id: i32 }
function g(): i32 {
    let n: i32 = 7;
    let h: H = H { f: (x: i32): i32 => { return n + x; }, id: 4 };
    return h.f(1) + h.id;
}
function main(): i32 { return g(); }`},
	// Regression: annotated rebind of the fn-field result (already worked). 6.
	{"fnfield-rebind-regress", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    let s: string = "hi";
    let h: H = H { f: (x: i32): string => { return s; }, id: 4 };
    let r: string = h.f(1);
    return r.len() + h.id;
}
function main(): i32 { return g(); }`},
}

// TestSelfHostTupleDestructureNestedStrIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
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
