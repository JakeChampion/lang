package e2ecompiler

import "testing"

var dynTraitIRCases = []struct {
	name     string
	src      string
	expected int
	static   bool
}{
	{name: "heterogeneous-array",
		src: `trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function sum(xs: dyn Shape[]): i32 { let t: i32 = 0; for x in xs { t = t + x.area(); } return t; } function main(): i32 { let xs: dyn Shape[] = [Circle { r: 3 }, Rect { w: 2, h: 5 }]; return sum(xs); }`, expected: 19},
	{name: "param-circle",
		src: `trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function ar(s: dyn Shape): i32 { return s.area(); } @noinline function n(k: i32): i32 { return k; } function main(): i32 { let c: Circle = Circle { r: n(4) }; return ar(c); }`, expected: 16},
	{name: "param-rect",
		src: `trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function ar(s: dyn Shape): i32 { return s.area(); } @noinline function n(k: i32): i32 { return k; } function main(): i32 { let r: Rect = Rect { w: n(2), h: 5 }; return ar(r); }`, expected: 10},
	{name: "method-with-arg",
		src: `trait Sc { function sc(self: Self, k: i32): i32; } struct A { v: i32 } struct B { v: i32 } impl Sc for A { function sc(self: Self, k: i32): i32 { return self.v * k; } } impl Sc for B { function sc(self: Self, k: i32): i32 { return self.v + k; } } function f(s: dyn Sc): i32 { return s.sc(3); } @noinline function n(k: i32): i32 { return k; } function main(): i32 { let a: A = A { v: n(5) }; return f(a); }`, expected: 15},
	{name: "three-impls",
		src: `trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } struct Unit { } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } impl Shape for Unit { function area(self: Self): i32 { return 7; } } function sum(xs: dyn Shape[]): i32 { let t: i32 = 0; for x in xs { t = t + x.area(); } return t; } function main(): i32 { let xs: dyn Shape[] = [Circle { r: 3 }, Rect { w: 2, h: 5 }, Unit { }]; return sum(xs); }`, expected: 26},

	{name: "prim-i32-scalar",
		src: `trait Show { function show(self: Self): i32; } impl Show for i32 { function show(self: Self): i32 { return self + 1; } } function run(s: dyn Show): i32 { return s.show(); } function main(): i32 { let x: i32 = 41; return run(x); }`, expected: 42},
	{name: "prim-i32-method-arg",
		src: `trait Sc { function sc(self: Self, k: i32): i32; } impl Sc for i32 { function sc(self: Self, k: i32): i32 { return self * k; } } function f(s: dyn Sc): i32 { return s.sc(3); } function main(): i32 { let a: i32 = 5; return f(a); }`, expected: 15},
	{name: "prim-string",
		src: `trait Show { function show(self: Self): i32; } impl Show for string { function show(self: Self): i32 { return self.len(); } } function run(s: dyn Show): i32 { return s.show(); } function main(): i32 { let x: string = "hello"; return run(x); }`, expected: 5},
	{name: "prim-i32-array",
		src: `trait Show { function show(self: Self): i32; } impl Show for i32 { function show(self: Self): i32 { return self; } } function sum(xs: dyn Show[]): i32 { let t: i32 = 0; for x in xs { t = t + x.show(); } return t; } function main(): i32 { let xs: dyn Show[] = [3, 4, 5]; return sum(xs); }`, expected: 12},

	{name: "prim-i32-var-init",
		src: `trait Show { function show(self: Self): i32; } impl Show for i32 { function show(self: Self): i32 { return self + 1; } } function main(): i32 { let d: dyn Show = 41; return d.show(); }`, expected: 42},
	{name: "struct-var-init",
		src: `trait Show { function show(self: Self): i32; } struct Circle { r: i32 } impl Show for Circle { function show(self: Self): i32 { return self.r * self.r; } } @noinline function n(k: i32): i32 { return k; } function main(): i32 { let d: dyn Show = Circle { r: n(4) }; return d.show(); }`, expected: 16},
	{name: "prim-i32-assign",
		src: `trait Show { function show(self: Self): i32; } impl Show for i32 { function show(self: Self): i32 { return self + 1; } } function main(): i32 { let d: dyn Show = 10; d = 41; return d.show(); }`, expected: 42},
	{name: "prim-i32-return",
		src: `trait Show { function show(self: Self): i32; } impl Show for i32 { function show(self: Self): i32 { return self; } } function pick(): dyn Show { return 5; } function main(): i32 { let d: dyn Show = pick(); return d.show(); }`, expected: 5},

	{name: "downcast-hit",
		src: `trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function dc(s: dyn Shape): i32 { match (s as? Circle) { Some(c) => { return c.r; }, None => { return 0; } } } function main(): i32 { let c: Circle = Circle { r: 7 }; return dc(c); }`, expected: 7},
	{name: "downcast-into-bounded-generic",
		src: `trait Tr { function m(self: Self, x: i32): i32; } struct S { n: i32 } impl Tr for S { function m(self: Self, x: i32): i32 { return self.n + x; } } function tg[T: Tr](a: T, b: T, x: i32): i32 { return a.m(x) * 3 + b.m(1); } function main(): i32 { let ds: dyn Tr[] = [S { n: 2 }, S { n: 3 }]; return match (ds[1] as? S) { Some(s) => tg(s, s, 4), None => 5 }; }`, expected: 25},
	{name: "downcast-miss",
		src: `trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function dc(s: dyn Shape): i32 { match (s as? Circle) { Some(c) => { return c.r; }, None => { return 0; } } } function main(): i32 { let r: Rect = Rect { w: 2, h: 5 }; return dc(r); }`, expected: 0, static: true},
	{name: "downcast-miss-dynamic",
		src: `trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function dc(s: dyn Shape): i32 { match (s as? Circle) { Some(c) => { return c.r; }, None => { return 0; } } } @noinline function n(k: i32): i32 { return k; } function main(): i32 { let r: Rect = Rect { w: n(2), h: 5 }; return dc(r); }`, expected: 0},
	{name: "downcast-hit-rect",
		src: `trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function dc(s: dyn Shape): i32 { match (s as? Rect) { Some(re) => { return re.w * re.h; }, None => { return 0; } } } function main(): i32 { let r: Rect = Rect { w: 2, h: 5 }; return dc(r); }`, expected: 10},
	{name: "downcast-enum-target",
		src: `enum Form { circle(i32), square(i32) } struct Dot { n: i32 } trait Shape { function area(self: Self): i32; } impl Shape for Form { function area(self: Self): i32 { match (self) { circle(r) => { return r; }, square(w) => { return w * w; } } } } impl Shape for Dot { function area(self: Self): i32 { return self.n; } } function to_form(s: dyn Shape): i32 { match (s as? Form) { Some(f) => { return f.area(); }, None => { return 0; } } } function main(): i32 { return to_form(Form.circle(5)) + to_form(Form.square(2)) * 10 + to_form(Dot { n: 4 }) * 100; }`, expected: 45},
	{name: "downcast-string-owner-miss",
		src: `import "std/string";
struct Named { s: string } struct Plain { n: i32 } trait Tag { function t(self: Self): i32; } impl Tag for Named { function t(self: Self): i32 { return self.s.len(); } } impl Tag for Plain { function t(self: Self): i32 { return self.n; } } function probe(d: dyn Tag): i32 { match (d as? Plain) { Some(p) => { return p.n; }, None => { return 100 + d.t(); } } } function main(): i32 { let a: i32 = probe(Named { s: "ab" + "cd" }); let b: i32 = probe(Plain { n: 7 }); return a - 100 + b * 10; }`, expected: 74},
	{name: "downcast-binding",
		src: `trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function radius(s: dyn Shape): i32 { let o: Option[Circle] = s as? Circle; match (o) { Some(c) => { return c.r; }, None => { return 0; } } } function main(): i32 { return radius(Circle { r: 6 }) + radius(Rect { w: 2, h: 3 }) * 10; }`, expected: 6},
	{name: "downcast-array-count",
		src: `trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function count(xs: dyn Shape[]): i32 { let n: i32 = 0; for x in xs { match (x as? Circle) { Some(c) => { n = n + 1; }, None => { } } } return n; } function main(): i32 { let xs: dyn Shape[] = [Circle { r: 3 }, Rect { w: 2, h: 5 }, Circle { r: 1 }]; return count(xs); }`, expected: 2},

	{name: "downcast-multi-hit",
		src: `trait Show { function show(self: Self): i32; } trait Weigh { function weight(self: Self): i32; } struct Apple { g: i32 } struct Brick { kg: i32 } impl Show for Apple { function show(self: Self): i32 { return self.g; } } impl Weigh for Apple { function weight(self: Self): i32 { return self.g; } } impl Show for Brick { function show(self: Self): i32 { return self.kg; } } impl Weigh for Brick { function weight(self: Self): i32 { return self.kg; } } function dc(d: dyn Show + Weigh): i32 { match (d as? Apple) { Some(a) => { return a.g; }, None => { return 0; } } } function main(): i32 { let x: Apple = Apple { g: 7 }; return dc(x); }`, expected: 7},
	{name: "downcast-multi-miss",
		src: `trait Show { function show(self: Self): i32; } trait Weigh { function weight(self: Self): i32; } struct Apple { g: i32 } struct Brick { kg: i32 } impl Show for Apple { function show(self: Self): i32 { return self.g; } } impl Weigh for Apple { function weight(self: Self): i32 { return self.g; } } impl Show for Brick { function show(self: Self): i32 { return self.kg; } } impl Weigh for Brick { function weight(self: Self): i32 { return self.kg; } } function dc(d: dyn Show + Weigh): i32 { match (d as? Apple) { Some(a) => { return a.g; }, None => { return 99; } } } function main(): i32 { let x: Brick = Brick { kg: 3 }; return dc(x); }`, expected: 99, static: true},
	{name: "downcast-multi-miss-dynamic",
		src: `trait Show { function show(self: Self): i32; } trait Weigh { function weight(self: Self): i32; } struct Apple { g: i32 } struct Brick { kg: i32 } impl Show for Apple { function show(self: Self): i32 { return self.g; } } impl Weigh for Apple { function weight(self: Self): i32 { return self.g; } } impl Show for Brick { function show(self: Self): i32 { return self.kg; } } impl Weigh for Brick { function weight(self: Self): i32 { return self.kg; } } function dc(d: dyn Show + Weigh): i32 { match (d as? Apple) { Some(a) => { return a.g; }, None => { return 99; } } } @noinline function n(k: i32): i32 { return k; } function main(): i32 { let x: Brick = Brick { kg: n(3) }; return dc(x); }`, expected: 99},

	{name: "dyn-string-len-chained",
		src: `trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function ids(s: string): string { return s; } function main(): i32 { let p: P = P { tag: ids("hello") }; let d: dyn Named = p; return d.name().len(); }`, expected: 5},
	{name: "dyn-string-len-heap",
		src: `trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function ids(s: string): string { return s; } function main(): i32 { let p: P = P { tag: ids("this is a long string value") }; let d: dyn Named = p; return d.name().len(); }`, expected: 27},
	{name: "dyn-string-len-via-var",
		src: `trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function ids(s: string): string { return s; } function main(): i32 { let p: P = P { tag: ids("hello") }; let d: dyn Named = p; let s: string = d.name(); return s.len(); }`, expected: 5},
	{name: "dyn-string-concat-chained",
		src: `trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function main(): i32 { let p: P = P { tag: "ab" }; let d: dyn Named = p; return (d.name() + "cd").len(); }`, expected: 4},
	{name: "dyn-string-len-param",
		src: `trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function f(d: dyn Named): i32 { return d.name().len(); } function ids(s: string): string { return s; } function main(): i32 { let p: P = P { tag: ids("hiya") }; return f(p); }`, expected: 4},
	{name: "dyn-i32-vs-unrelated-string",
		src: `trait Foo { function bar(self: Self): i32; } struct A { n: i32 } impl Foo for A { function bar(self: Self): i32 { return self.n; } } struct S { s: string } function (self: S) bar(): string { return self.s; } function ids(s: string): string { return s; } function main(): i32 { let a: A = A { n: 9 }; let d: dyn Foo = a; let sv: S = S { s: ids("xyz") }; return d.bar() + sv.bar().len(); }`, expected: 12},

	{name: "dyn-i64-chained",
		src: `trait Big { function v(self: Self): i64; } struct P { n: i64 } impl Big for P { function v(self: Self): i64 { return self.n; } } function main(): i32 { let p: P = P { n: 5000000000 }; let d: dyn Big = p; let r: i64 = d.v() + 1; if (r == 5000000001) { return 1; } return 0; }`, expected: 1, static: true},
	{name: "dyn-u64-chained",
		src: `trait U { function v(self: Self): u64; } struct P { n: u64 } impl U for P { function v(self: Self): u64 { return self.n; } } function main(): i32 { let p: P = P { n: 5000000000 }; let d: dyn U = p; let r: u64 = d.v() + 1; if (r == 5000000001) { return 1; } return 0; }`, expected: 1, static: true},
	{name: "dyn-f64-chained",
		src: `trait Fl { function f(self: Self): f64; } struct S { v: f64 } impl Fl for S { function f(self: Self): f64 { return self.v; } } function main(): i32 { let s: S = S { v: 2.5 }; let d: dyn Fl = s; let r: f64 = d.f() + 0.5; if (r == 3.0) { return 1; } return 0; }`, expected: 1, static: true},
	{name: "dyn-f32-chained",
		src: `trait F { function v(self: Self): f32; } struct P { n: f32 } impl F for P { function v(self: Self): f32 { return self.n; } } function main(): i32 { let p: P = P { n: 2.5 }; let d: dyn F = p; let r: f32 = d.v() + 0.5; if (r == 3.0) { return 1; } return 0; }`, expected: 1},
	{name: "dyn-array-len-chained",
		src: `trait Arr { function make(self: Self): i32[]; } struct S { n: i32 } impl Arr for S { function make(self: Self): i32[] { return [self.n, self.n, self.n]; } } function main(): i32 { let s: S = S { n: 1 }; let d: dyn Arr = s; return d.make().len(); }`, expected: 3},

	{name: "dyn-arr-f64-direct-index",
		src: `trait Arr { function make(self: Self): f64[]; } struct S { v: f64 } impl Arr for S { function make(self: Self): f64[] { return [self.v, self.v + 1.0]; } } function main(): i32 { let s: S = S { v: 2.5 }; let d: dyn Arr = s; let r: f64 = d.make()[1] + 1.0; if (r == 4.5) { return 1; } return 0; }`, expected: 1},
	{name: "dyn-arr-string-direct-index",
		src: `trait Arr { function make(self: Self): string[]; } struct S { s: string } impl Arr for S { function make(self: Self): string[] { return [self.s, "hello"]; } } function main(): i32 { let s: S = S { s: "ab" }; let d: dyn Arr = s; return d.make()[1].len(); }`, expected: 5},
}

func TestSelfHostDynTraitIR(t *testing.T) {
	boxedProbes(t)
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range dynTraitIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				stderr, code := cli.exitOf(t, tc.src, target, "FERN_LEAKCHECK=1")
				if code != tc.expected {
					t.Fatalf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
				if tc.static {
					allocs, frees, live := leakSummaryOf(t, tc.name, stderr)
					if allocs != 0 || frees != 0 || live != 0 {
						t.Fatalf("static probe: allocs=%d frees=%d live=%d, want all zero", allocs, frees, live)
					}
				} else {
					assertBalancedCensus(t, stderr)
				}
			})
		}
	}
}
