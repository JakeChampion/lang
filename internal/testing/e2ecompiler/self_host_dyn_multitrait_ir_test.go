package e2ecompiler

import "testing"

var dynMultiTraitIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"two-trait-scalar",
		`trait Show { function show(self: Self): i32; } trait Weigh { function weigh(self: Self): i32; } struct Apple { w: i32 } impl Show for Apple { function show(self: Self): i32 { return 10; } } impl Weigh for Apple { function weigh(self: Self): i32 { return self.w; } } function describe(x: dyn Show + Weigh): i32 { return x.show() + x.weigh(); } function main(): i32 { let a: Apple = Apple { w: 3 }; return describe(a); }`, 13},
	{"two-trait-scalar-reordered",
		`trait Show { function show(self: Self): i32; } trait Weigh { function weigh(self: Self): i32; } struct Apple { w: i32 } impl Show for Apple { function show(self: Self): i32 { return 10; } } impl Weigh for Apple { function weigh(self: Self): i32 { return self.w; } } function describe(x: dyn Weigh + Show): i32 { return x.show() + x.weigh(); } function main(): i32 { let a: Apple = Apple { w: 3 }; return describe(a); }`, 13},
	{"two-trait-array",
		`trait Show { function show(self: Self): i32; } trait Weigh { function weigh(self: Self): i32; } struct Apple { w: i32 } struct Brick { kg: i32 } impl Show for Apple { function show(self: Self): i32 { return 10; } } impl Weigh for Apple { function weigh(self: Self): i32 { return self.w; } } impl Show for Brick { function show(self: Self): i32 { return 20; } } impl Weigh for Brick { function weigh(self: Self): i32 { return self.kg; } } function total(xs: dyn Show + Weigh[]): i32 { let t: i32 = 0; for x in xs { t = t + x.show() + x.weigh(); } return t; } function main(): i32 { let xs: dyn Show + Weigh[] = [Apple { w: 3 }, Brick { kg: 5 }]; return total(xs); }`, 38},
	{"three-trait-scalar",
		`trait A { function a(self: Self): i32; } trait B { function b(self: Self): i32; } trait C { function c(self: Self): i32; } struct T { v: i32 } impl A for T { function a(self: Self): i32 { return 1; } } impl B for T { function b(self: Self): i32 { return self.v * 2; } } impl C for T { function c(self: Self): i32 { return 100; } } function f(x: dyn A + B + C): i32 { return x.a() + x.b() + x.c(); } function main(): i32 { let t: T = T { v: 4 }; return f(t); }`, 109},
	{"two-trait-method-arg",
		`trait Sc { function scale(self: Self, k: i32): i32; } trait Tag { function tag(self: Self): i32; } struct W { v: i32 } impl Sc for W { function scale(self: Self, k: i32): i32 { return self.v * k; } } impl Tag for W { function tag(self: Self): i32 { return 7; } } function f(x: dyn Sc + Tag): i32 { return x.scale(3) + x.tag(); } function main(): i32 { let w: W = W { v: 5 }; return f(w); }`, 22},
	{"single-trait-regression",
		`trait Show { function show(self: Self): i32; } struct Circle { r: i32 } impl Show for Circle { function show(self: Self): i32 { return self.r * self.r; } } function f(s: dyn Show): i32 { return s.show(); } function main(): i32 { let c: Circle = Circle { r: 4 }; return f(c); }`, 16},
}

func TestSelfHostDynMultiTraitIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range dynMultiTraitIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}
