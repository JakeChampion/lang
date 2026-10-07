package e2ecompiler

import "testing"

// dynFnParamIRCases exercise a `dyn Trait` value dispatched THROUGH a
// function-typed parameter — a `(dyn Trait) => R` fn value called with a
// trait-object argument — on the self-host IR path (x86-64 + wasm).
//
// The `(dyn Trait) => R` fn-type spelling parses to the coarse "fn" tag. The
// shapes below (#5276):
//
//   - a struct-backed `dyn Trait` value flows UNBOXED (it already carries its
//     shape pointer at offset 0), so passing it through `f(x)` — whether the
//     value arrives pre-coerced in a `dyn Trait` param or is coerced inline at
//     the fn-value call site — carries the shape and `s.area()` dispatches via
//     op_dyn_dispatch inside the callee;
//   - a primitive-backed `dyn Trait` value coerced at the OUTER call
//     (`apply(speak_of, q)` where `q: dyn Speak`) is heap-boxed at that call,
//     so the box pointer flows through `f(x)` unchanged and dispatches
//     correctly.
//
// A PRIMITIVE literal coerced to `dyn Trait` AT an indirect fn-value call
// (`f(7)` where `f: (dyn Speak) => i32`) is not one of these cases.
//
// Each case is oracle-checked against the interpreter, returning a
// non-negative value <= 126 (cf. #2908).
var dynFnParamIRCases = []struct {
	name string
	main string
}{
	// The #5276 repro: a struct-backed `dyn Shape` value coerced at the outer
	// call, dispatched through the `(dyn Shape) => i32` fn param. 4*4 = 16.
	{"repro-struct-via-param", `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
function apply(f: (dyn Shape) => i32, x: dyn Shape): i32 { return f(x); }
function area_of(s: dyn Shape): i32 { return s.area(); }
function main(): i32 { let q: dyn Shape = Sq{s:4}; return apply(area_of, q); }`},
	// Two impls so dispatch is meaningful, value via the `dyn Shape` param:
	// Rect{3,5}.area() = 15.
	{"two-impl-via-param", `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
struct Rect { w: i32, h: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } }
function apply(f: (dyn Shape) => i32, x: dyn Shape): i32 { return f(x); }
function area_of(s: dyn Shape): i32 { return s.area(); }
function main(): i32 { let q: dyn Shape = Rect{w:3,h:5}; return apply(area_of, q); }`},
	// A struct value coerced to `dyn Shape` INLINE at the indirect fn-value
	// call (`f(Rect{..})`), no intermediate `dyn` param: 3*5 = 15.
	{"struct-inline-at-indirect", `trait Shape { function area(self: Self): i32; }
struct Rect { w: i32, h: i32 }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } }
function apply(f: (dyn Shape) => i32): i32 { return f(Rect{w:3,h:5}); }
function area_of(s: dyn Shape): i32 { return s.area(); }
function main(): i32 { return apply(area_of); }`},
	// A primitive-backed `dyn Speak` value: boxed at the outer `apply(speak_of, q)`
	// call, dispatched through the fn param. 7 + 100 = 107.
	{"prim-via-param", `trait Speak { function say(self: Self): i32; }
impl Speak for i32 { function say(self: Self): i32 { return self + 100; } }
function apply(f: (dyn Speak) => i32, x: dyn Speak): i32 { return f(x); }
function speak_of(s: dyn Speak): i32 { return s.say(); }
function main(): i32 { let q: dyn Speak = 7; return apply(speak_of, q); }`},
}

// TestSelfHostDynFnParamIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostDynFnParamIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range dynFnParamIRCases {
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
