package e2eselfhost

import "testing"

// tupleDestructureNestedStrIRCases pin the two #5306 gaps — string elements of
// a tuple destructure that miscompiled ON the IR path (silent wrong answers,
// not bails; native/interp is the oracle):
//
//  1. NESTED destructure: `var (p, c) = t; var (s, b) = p` over
//     ((string, i32), i32) read the string element empty (11 → 9). The
//     destructure binding chain never recorded a tuple-typed element's
//     element tags, so the second-level destructure resolved dtag "" and the
//     string binding was never str-marked (the all-i32 shape only worked
//     because the untyped fallback happens to be i32). Fixed by the
//     mark_tuple_elems branch in the destructure chain.
//
//  2. A struct fn-FIELD closure call chained into a string op:
//     `h.f(1).len()` where H.f: (i32) => string read 0 (6 → 4) — for ANY
//     captured or literal string return, destructure or not. The field's
//     declared type coarsens to "fn" (losing the return), so expr_is_str
//     didn't know the call yields a string. Fixed by preserving a `string`
//     fn return in StructFieldDecl.fn_ret (parser) and consuming it in
//     expr_is_str's fn-field-call arm. (An annotated rebind
//     `var r: string = h.f(1)` already worked — only the direct chain broke.)
//
// Each case is oracle-checked against the interpreter; results stay <= 120
// (the wasm exit-code clamp, #2908).
var tupleDestructureNestedStrIRCases = []struct {
	name string
	main string
}{
	// Gap 1: nested string destructure. 11 (was 9 — s.len() read 0).
	{"nested-str", `function g(): i32 {
    var t: ((string, i32), i32) = (("hi", 4), 5);
    var (p, c) = t;
    var (s, b) = p;
    return s.len() + b + c;
}
function main(): i32 { return g(); }`},
	// Gap 1 sibling: read the nested element via p.N instead of a second
	// destructure (the recorded element tags drive both). 11.
	{"nested-str-dotn", `function g(): i32 {
    var t: ((string, i32), i32) = (("hi", 4), 5);
    var (p, c) = t;
    return p.0.len() + p.1 + c;
}
function main(): i32 { return g(); }`},
	// Gap 1 regression: the all-i32 nested shape (#5201) still works. 16.
	{"nested-i32-regress", `function g(): i32 {
    var t: ((i32, i32), i32) = ((7, 4), 5);
    var (p, c) = t;
    var (a, b) = p;
    return a + b + c;
}
function main(): i32 { return g(); }`},
	// Gap 2: struct fn-field closure call chained into .len(), capturing a
	// destructure-bound string. 6 (was 4 — the .len() leg read 0).
	{"fnfield-captured-destructure-str", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    var t: (string, i32) = ("hi", 4);
    var (s, b) = t;
    var h: H = H { f: (x: i32): string => { return s; }, id: b };
    return h.f(1).len() + h.id;
}
function main(): i32 { return g(); }`},
	// Gap 2, plain-local capture (the same break, no destructure involved). 6.
	{"fnfield-captured-str", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    var s: string = "hi";
    var h: H = H { f: (x: i32): string => { return s; }, id: 4 };
    return h.f(1).len() + h.id;
}
function main(): i32 { return g(); }`},
	// Gap 2, no capture — a literal string return through the fn field. 6.
	{"fnfield-literal-str", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    var h: H = H { f: (x: i32): string => { return "hi"; }, id: 4 };
    return h.f(1).len() + h.id;
}
function main(): i32 { return g(); }`},
	// Gap 2 concat shape: the fn-field call result feeds `+` as a string. 8.
	{"fnfield-str-concat", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    var h: H = H { f: (x: i32): string => { return "hi"; }, id: 4 };
    return (h.f(1) + "!!").len() + h.id;
}
function main(): i32 { return g(); }`},
	// Regression: an i32-returning fn field is untouched by the fn_ret
	// threading. 12.
	{"fnfield-i32-regress", `struct H { f: (i32) => i32, id: i32 }
function g(): i32 {
    var n: i32 = 7;
    var h: H = H { f: (x: i32): i32 => { return n + x; }, id: 4 };
    return h.f(1) + h.id;
}
function main(): i32 { return g(); }`},
	// Regression: annotated rebind of the fn-field result (already worked). 6.
	{"fnfield-rebind-regress", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    var s: string = "hi";
    var h: H = H { f: (x: i32): string => { return s; }, id: 4 };
    var r: string = h.f(1);
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
