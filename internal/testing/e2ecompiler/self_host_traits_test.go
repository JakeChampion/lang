package e2ecompiler

import "testing"

var traitsCases = []struct {
	name string
	src  string
	exit int
}{
	{"trait-impl-method",
		"trait Area { function area(self: Self): i32; } " +
			"struct Sq { side: i32 } " +
			"impl Area for Sq { function area(self: Self): i32 { return self.side * self.side; } } " +
			"function main(): i32 { let s: Sq = Sq { side: 5 }; return s.area(); }", 25},
	{"trait-impl-arg",
		"trait Adder { function add(self: Self, other: Self): i32; } " +
			"struct N { v: i32 } " +
			"impl Adder for N { function add(self: Self, other: Self): i32 { return self.v + other.v; } } " +
			"function main(): i32 { let a: N = N { v: 19 }; let b: N = N { v: 23 }; return a.add(b); }", 42},
	{"trait-two-impls",
		"trait Tag { function tag(self: Self): i32; } " +
			"struct A { x: i32 } struct B { y: i32 } " +
			"impl Tag for A { function tag(self: Self): i32 { return self.x; } } " +
			"impl Tag for B { function tag(self: Self): i32 { return self.y + 1; } } " +
			"function main(): i32 { let a: A = A { x: 10 }; let b: B = B { y: 31 }; return a.tag() + b.tag(); }", 42},
	{"trait-bounded-generic-monotype",
		"pub trait Area { function area(self: Self): i32; } " +
			"struct Sq { side: i32 } " +
			"impl Area for Sq { function area(self: Self): i32 { return self.side * self.side; } } " +
			"function describe[T: Area](v: T): i32 { return v.area(); } " +
			"function main(): i32 { let s: Sq = Sq { side: 6 }; return describe(s); }", 36},
	{"trait-bounded-generic-multitype",
		"trait Show { function show(self: Self): i32; } " +
			"struct A { x: i32 } struct B { y: i32 } " +
			"impl Show for A { function show(self: Self): i32 { return self.x; } } " +
			"impl Show for B { function show(self: Self): i32 { return self.y; } } " +
			"function describe[T: Show](v: T): i32 { return v.show(); } " +
			"function main(): i32 { let a: A = A { x: 7 }; let b: B = B { y: 4 }; return describe(a) + describe(b); }", 11},
	{"trait-bounded-generic-primitive",
		"trait Eq { function eq(self: Self, other: Self): boolean; } " +
			"impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } " +
			"function same[T: Eq](a: T, b: T): i32 { if (a.eq(b)) { return 1; } return 0; } " +
			"function main(): i32 { let n: i32 = 5; return same(n, 5) + same(n, 6); }", 1},
	{"trait-bounded-generic-mixed",
		"trait Sized { function sz(self: Self): i32; } " +
			"struct Boxx { v: i32 } " +
			"impl Sized for i32 { function sz(self: Self): i32 { return self; } } " +
			"impl Sized for Boxx { function sz(self: Self): i32 { return self.v; } } " +
			"function getsz[T: Sized](x: T): i32 { return x.sz(); } " +
			"function main(): i32 { let b: Boxx = Boxx { v: 30 }; let n: i32 = 12; return getsz(n) + getsz(b); }", 42},
	{"trait-bounded-generic-array-elem",
		"trait Eq { function eq(self: Self, other: Self): boolean; } " +
			"impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } " +
			"function all_eq[T: Eq](a: T[], b: T[]): i32 { let i: i32 = 0; while (i < a.len()) { if (!a[i].eq(b[i])) { return 0; } i = i + 1; } return 1; } " +
			"function main(): i32 { let x: i32[] = [1, 2, 3]; let y: i32[] = [1, 2, 3]; return all_eq(x, y); }", 1},
	{"trait-bounded-generic-two-params",
		"trait Show { function show(self: Self): i32; } " +
			"struct A { x: i32 } struct B { y: i32 } " +
			"impl Show for A { function show(self: Self): i32 { return self.x; } } " +
			"impl Show for B { function show(self: Self): i32 { return self.y; } } " +
			"function combine[P: Show, Q: Show](p: P, q: Q): i32 { return p.show() + q.show(); } " +
			"function main(): i32 { let a: A = A { x: 30 }; let b: B = B { y: 12 }; return combine(a, b); }", 42},
	{"trait-parametric-impl-struct-elem",
		"trait Valued { function val(self: Self): i32; } " +
			"struct Inner { n: i32 } " +
			"impl Valued for Inner { function val(self: Self): i32 { return self.n; } } " +
			"struct Box[T] { v: T } " +
			"impl[T: Valued] Valued for Box[T] { function val(self: Self): i32 { return self.v.val() + 1; } } " +
			"function main(): i32 { let b: Box[Inner] = Box { v: Inner { n: 41 } }; return b.val(); }", 42},
	{"trait-dyn-object-heterogeneous",
		"trait Shape { function area(self: Self): i32; } " +
			"struct Circle { r: i32 } struct Rect { w: i32, h: i32 } " +
			"impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } " +
			"impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } " +
			"function sum(xs: dyn Shape[]): i32 { let t: i32 = 0; for x in xs { t = t + x.area(); } return t; } " +
			"function main(): i32 { let xs: dyn Shape[] = [Circle { r: 3 }, Rect { w: 2, h: 5 }]; return sum(xs); }", 19},
	{"trait-struct-array-loop-method",
		"struct P { v: i32 } function (p: P) get(): i32 { return p.v; } " +
			"function main(): i32 { let ps: P[] = [P { v: 3 }, P { v: 4 }]; let t: i32 = 0; for x in ps { t = t + x.get(); } return t; }", 7},
	{"trait-derive-struct-eq",
		"trait Eq { function eq(self: Self, other: Self): boolean; } " +
			"impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } " +
			"@derive(Eq) struct P { x: i32, y: i32 } " +
			"function main(): i32 { let a: P = P { x: 1, y: 2 }; let b: P = P { x: 1, y: 2 }; let c: P = P { x: 1, y: 9 }; " +
			"let r: i32 = 0; if (a.eq(b)) { r = r + 1; } if (!a.eq(c)) { r = r + 2; } return r; }", 3},
	{"trait-derive-struct-ord",
		"trait Ord { function cmp(self: Self, other: Self): i32; } " +
			"impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } } " +
			"@derive(Ord) struct P { x: i32, y: i32 } " +
			"function main(): i32 { let a: P = P { x: 1, y: 2 }; let c: P = P { x: 1, y: 9 }; " +
			"if (a.cmp(c) < 0) { if (c.cmp(a) > 0) { if (a.cmp(a) == 0) { return 42; } } } return 0; }", 42},
	{"trait-derive-struct-display-nested",
		"import \"core/cmp\"; @derive(cmp.Display) struct Inner { n: i32 } " +
			"@derive(cmp.Display) struct Outer { a: Inner, tag: string } " +
			"function main(): i32 { let p: Outer = Outer { a: Inner { n: 5 }, tag: \"hi\" }; return p.to_string().len(); }",
		len("Outer { a: Inner { n: 5 }, tag: hi }")},
	{"trait-enum-method",
		"enum Shape { Circle(i32), Square(i32) } " +
			"function (s: Shape) area(): i32 { match (s) { Circle(r) => { return r * r * 3; }, Square(w) => { return w * w; } } } " +
			"function main(): i32 { let a: Shape = Circle(3); let b: Shape = Square(4); return a.area() + b.area(); }", 43},
	{"trait-enum-method-unannot-local",
		"enum Shape { Circle(i32), Square(i32) } " +
			"function (s: Shape) area(): i32 { match (s) { Circle(r) => { return r * r * 3; }, Square(w) => { return w * w; } } } " +
			"function main(): i32 { let a = Circle(3); let b = Square(4); return a.area() + b.area(); }", 43},
	{"trait-enum-method-unannot-payloadless",
		"enum Color { Red, Green } " +
			"function (c: Color) code(): i32 { match (c) { Red => { return 1; }, Green => { return 2; } } } " +
			"function main(): i32 { let c = Green; let d = Red; return c.code() * 10 + d.code(); }", 21},
	{"trait-derive-enum-display",
		"import \"core/cmp\"; @derive(cmp.Display) enum Opt { Has(i32), Nil } " +
			"function main(): i32 { let h: Opt = Has(7); let n: Opt = Nil; return h.to_string().len() + n.to_string().len(); }",
		len("Has(7)") + len("Nil")},
	{"trait-derive-enum-eq",
		"trait Eq { function eq(self: Self, other: Self): boolean; } " +
			"impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } " +
			"@derive(Eq) enum Opt { Has(i32), Nil } " +
			"function main(): i32 { let r: i32 = 0; " +
			"if (Has(5).eq(Has(5))) { r = r + 1; } if (!Has(5).eq(Has(6))) { r = r + 2; } " +
			"if (!Has(5).eq(Nil)) { r = r + 4; } if (Nil.eq(Nil)) { r = r + 8; } return r; }", 15},
	{"trait-derive-enum-ord",
		"trait Ord { function cmp(self: Self, other: Self): i32; } " +
			"impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } } " +
			"@derive(Ord) enum Lvl { Low(i32), High } " +
			"function main(): i32 { let r: i32 = 0; " +
			"if (Low(1).cmp(Low(2)) < 0) { r = r + 1; } if (Low(9).cmp(High) < 0) { r = r + 2; } " +
			"if (High.cmp(Low(0)) > 0) { r = r + 4; } if (Low(3).cmp(Low(3)) == 0) { r = r + 8; } return r; }", 15},
	{"trait-derive-struct-json",
		"import \"std/i32\"; trait Json { function to_json(self: Self): string; } " +
			"impl Json for i32 { function to_json(self: Self): string { return self.to_string(); } } " +
			"impl Json for string { function to_json(self: Self): string { return \"\\\"\" + self + \"\\\"\"; } } " +
			"@derive(Json) struct Item { id: i32, tag: string } " +
			"function main(): i32 { let p: Item = Item { id: 7, tag: \"hi\" }; return p.to_json().len(); }",
		len(`{"id":7,"tag":"hi"}`)},
	{"trait-derive-enum-json",
		"import \"std/i32\"; trait Json { function to_json(self: Self): string; } " +
			"impl Json for i32 { function to_json(self: Self): string { return self.to_string(); } } " +
			"@derive(Json) enum Opt { Has(i32), Nil } " +
			"function main(): i32 { let h: Opt = Has(7); let n: Opt = Nil; return h.to_json().len() + n.to_json().len(); }",
		len(`{"Has":7}`) + len(`"Nil"`)},
	{"trait-derive-struct-debug",
		"import \"core/cmp\"; @derive(cmp.Debug) struct Item { id: i32, tag: string } " +
			"function main(): i32 { let p: Item = Item { id: 7, tag: \"hi\" }; return p.to_debug().len(); }",
		len(`Item { id: 7, tag: "hi" }`)},
	{"trait-derive-enum-debug",
		"import \"core/cmp\"; @derive(cmp.Debug) enum Msg { Word(string), End } " +
			"function main(): i32 { let w: Msg = Word(\"hi\"); let e: Msg = End; return w.to_debug().len() + e.to_debug().len(); }",
		len(`Word("hi")`) + len("End")},
	{"trait-derive-struct-hash",
		"trait Hash { function hash(self: Self): i32; } " +
			"impl Hash for i32 { function hash(self: Self): i32 { return self; } } " +
			"@derive(Hash) struct P { x: i32, y: i32 } " +
			"function main(): i32 { let a: P = P { x: 3, y: 4 }; let b: P = P { x: 3, y: 4 }; let c: P = P { x: 5, y: 4 }; " +
			"let r: i32 = 0; if (a.hash() == b.hash()) { r = r + 1; } if (a.hash() != c.hash()) { r = r + 2; } return r; }", 3},
	{"trait-derive-enum-hash",
		"trait Hash { function hash(self: Self): i32; } " +
			"impl Hash for i32 { function hash(self: Self): i32 { return self; } } " +
			"@derive(Hash) enum Shape { Dot, Circle(i32) } " +
			"function main(): i32 { let r: i32 = 0; " +
			"if (Dot.hash() != Circle(0).hash()) { r = r + 1; } if (Circle(7).hash() == Circle(7).hash()) { r = r + 2; } return r; }", 3},
	{"trait-generic-struct-derive-display-i32",
		"import \"core/cmp\"; @derive(cmp.Display) struct Box[T] { v: T } " +
			"function main(): i32 { let b: Box[i32] = Box { v: 5 }; return b.to_string().len(); }",
		len("Box { v: 5 }")},
	{"trait-generic-struct-derive-display-string",
		"import \"core/cmp\"; @derive(cmp.Display) struct Box[T] { v: T } " +
			"function main(): i32 { let b: Box[string] = Box { v: \"hi\" }; return b.to_string().len(); }",
		len("Box { v: hi }")},
	{"trait-generic-struct-derive-display-both",
		"import \"core/cmp\"; @derive(cmp.Display) struct Box[T] { v: T } " +
			"function main(): i32 { let a: Box[i32] = Box { v: 5 }; let b: Box[string] = Box { v: \"hi\" }; " +
			"return a.to_string().len() + b.to_string().len(); }",
		len("Box { v: 5 }") + len("Box { v: hi }")},
	{"trait-generic-struct-derive-eq",
		"trait Eq { function eq(self: Self, other: Self): boolean; } " +
			"impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } " +
			"@derive(Eq) struct Box[T] { v: T } " +
			"function main(): i32 { let a: Box[i32] = Box { v: 5 }; let b: Box[i32] = Box { v: 5 }; let c: Box[i32] = Box { v: 9 }; " +
			"let r: i32 = 0; if (a.eq(b)) { r = r + 1; } if (!a.eq(c)) { r = r + 2; } return r; }", 3},
	{"trait-generic-struct-derive-ord",
		"trait Ord { function cmp(self: Self, other: Self): i32; } " +
			"impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } } " +
			"@derive(Ord) struct Box[T] { v: T } " +
			"function main(): i32 { let a: Box[i32] = Box { v: 1 }; let c: Box[i32] = Box { v: 9 }; " +
			"if (a.cmp(c) < 0) { if (c.cmp(a) > 0) { if (a.cmp(a) == 0) { return 42; } } } return 0; }", 42},
	{"trait-generic-struct-parametric-impl",
		"import \"std/i32\"; trait Show { function show(self: Self): string; } " +
			"impl Show for i32 { function show(self: Self): string { return self.to_string(); } } " +
			"impl Show for string { function show(self: Self): string { return self; } } " +
			"struct Box[T] { v: T } " +
			"impl[T: Show] Show for Box[T] { function show(self: Self): string { return \"Box(\" + self.v.show() + \")\"; } } " +
			"function main(): i32 { let a: Box[i32] = Box { v: 7 }; let b: Box[string] = Box { v: \"hi\" }; " +
			"return a.show().len() + b.show().len(); }",
		len("Box(7)") + len("Box(hi)")},
}

func TestSelfHostTraitsX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range traitsCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostTraitsArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range traitsCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
