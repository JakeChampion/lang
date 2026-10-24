package e2ecompiler

import "testing"

var dynFnParamIRCases = []struct {
	name string
	main string
}{
	{"repro-struct-via-param", `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
function apply(f: (dyn Shape) => i32, x: dyn Shape): i32 { return f(x); }
function area_of(s: dyn Shape): i32 { return s.area(); }
function main(): i32 { let q: dyn Shape = Sq{s:4}; return apply(area_of, q); }`},
	{"two-impl-via-param", `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
struct Rect { w: i32, h: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } }
function apply(f: (dyn Shape) => i32, x: dyn Shape): i32 { return f(x); }
function area_of(s: dyn Shape): i32 { return s.area(); }
function main(): i32 { let q: dyn Shape = Rect{w:3,h:5}; return apply(area_of, q); }`},
	{"struct-inline-at-indirect", `trait Shape { function area(self: Self): i32; }
struct Rect { w: i32, h: i32 }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } }
function apply(f: (dyn Shape) => i32): i32 { return f(Rect{w:3,h:5}); }
function area_of(s: dyn Shape): i32 { return s.area(); }
function main(): i32 { return apply(area_of); }`},
	{"prim-via-param", `trait Speak { function say(self: Self): i32; }
impl Speak for i32 { function say(self: Self): i32 { return self + 100; } }
function apply(f: (dyn Speak) => i32, x: dyn Speak): i32 { return f(x); }
function speak_of(s: dyn Speak): i32 { return s.say(); }
function main(): i32 { let q: dyn Speak = 7; return apply(speak_of, q); }`},
}

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
