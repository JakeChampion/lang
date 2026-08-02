package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// traitsCases cover trait / impl declarations through the self-hosted
// compiler. Concrete impls desugar (in the shared parser) to ordinary
// receiver-method FuncDecls dispatched by the receiver's runtime shape.
// Trait-BOUNDED generics are monomorphised in the parser's
// `monomorphize_module` pass — cloned per concrete call-site type so a
// method call on a `T`-typed value resolves to a concrete method (this
// is required for primitive receivers, whose unboxed values carry no
// shape pointer for dynamic dispatch). Exit codes are the behavioural
// contract. See docs/TRAITS.md §7a.
var traitsCases = []struct {
	name string
	src  string
	exit int
}{
	// Trait + impl on a struct, then a direct method call.
	{"trait-impl-method",
		"trait Area { function area(self: Self): i32; } " +
			"struct Sq { side: i32 } " +
			"impl Area for Sq { function area(self: Self): i32 { return self.side * self.side; } } " +
			"function main(): i32 { var s: Sq = Sq { side: 5 }; return s.area(); }", 25},
	// Impl method taking an extra argument (Self in a non-receiver slot).
	{"trait-impl-arg",
		"trait Adder { function add(self: Self, other: Self): i32; } " +
			"struct N { v: i32 } " +
			"impl Adder for N { function add(self: Self, other: Self): i32 { return self.v + other.v; } } " +
			"function main(): i32 { var a: N = N { v: 19 }; var b: N = N { v: 23 }; return a.add(b); }", 42},
	// Two impls of the same trait for two structs; each dispatches to
	// its own method.
	{"trait-two-impls",
		"trait Tag { function tag(self: Self): i32; } " +
			"struct A { x: i32 } struct B { y: i32 } " +
			"impl Tag for A { function tag(self: Self): i32 { return self.x; } } " +
			"impl Tag for B { function tag(self: Self): i32 { return self.y + 1; } } " +
			"function main(): i32 { var a: A = A { x: 10 }; var b: B = B { y: 31 }; return a.tag() + b.tag(); }", 42},
	// `pub trait` + a trait-bounded generic function. The bound is
	// discarded with the rest of the type-param list; the body's
	// `v.area()` resolves because the only call site passes a concrete
	// Sq, whose receiver method exists.
	{"trait-bounded-generic-monotype",
		"pub trait Area { function area(self: Self): i32; } " +
			"struct Sq { side: i32 } " +
			"impl Area for Sq { function area(self: Self): i32 { return self.side * self.side; } } " +
			"function describe[T: Area](v: T): i32 { return v.area(); } " +
			"function main(): i32 { var s: Sq = Sq { side: 6 }; return describe(s); }", 36},
	// ONE bounded-generic body called at TWO different concrete types →
	// monomorphised into two clones (describe__A, describe__B), each
	// dispatching `v.show()` to its own impl.
	{"trait-bounded-generic-multitype",
		"trait Show { function show(self: Self): i32; } " +
			"struct A { x: i32 } struct B { y: i32 } " +
			"impl Show for A { function show(self: Self): i32 { return self.x; } } " +
			"impl Show for B { function show(self: Self): i32 { return self.y; } } " +
			"function describe[T: Show](v: T): i32 { return v.show(); } " +
			"function main(): i32 { var a: A = A { x: 7 }; var b: B = B { y: 4 }; return describe(a) + describe(b); }", 11},
	// Primitive receiver through an erased bounded generic — the case
	// that crashed before monomorphisation (the dynamic shape-pointer
	// dispatch can't read a tag off an unboxed i32). The pass clones
	// `same` -> `same__i32` so the receiver's static type is concrete.
	{"trait-bounded-generic-primitive",
		"trait Eq { function eq(self: Self, other: Self): boolean; } " +
			"impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } " +
			"function same[T: Eq](a: T, b: T): i32 { if (a.eq(b)) { return 1; } return 0; } " +
			"function main(): i32 { var n: i32 = 5; return same(n, 5) + same(n, 6); }", 1},
	// One bounded generic instantiated at BOTH a primitive and a struct
	// in the same program — two distinct clones.
	{"trait-bounded-generic-mixed",
		"trait Sized { function sz(self: Self): i32; } " +
			"struct Boxx { v: i32 } " +
			"impl Sized for i32 { function sz(self: Self): i32 { return self; } } " +
			"impl Sized for Boxx { function sz(self: Self): i32 { return self.v; } } " +
			"function getsz[T: Sized](x: T): i32 { return x.sz(); } " +
			"function main(): i32 { var b: Boxx = Boxx { v: 30 }; var n: i32 = 12; return getsz(n) + getsz(b); }", 42},
	// Array-element method dispatch through a bounded generic: `a[i].eq`
	// must dispatch on the element type. Probe for the std/test array
	// collapse.
	{"trait-bounded-generic-array-elem",
		"trait Eq { function eq(self: Self, other: Self): boolean; } " +
			"impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } " +
			"function all_eq[T: Eq](a: T[], b: T[]): i32 { var i: i32 = 0; while (i < len(a)) { if (!a[i].eq(b[i])) { return 0; } i = i + 1; } return 1; } " +
			"function main(): i32 { var x: i32[] = [1, 2, 3]; var y: i32[] = [1, 2, 3]; return all_eq(x, y); }", 1},
	// TWO independent type parameters → the monomorphiser infers each
	// from its own argument and mangles the clone with both concrete
	// types joined (`combine__A__B`). This is the multi-parameter path
	// the std/test `Map[K, V]` assertion collapse relies on. See
	// docs/TRAITS.md §7a.
	{"trait-bounded-generic-two-params",
		"trait Show { function show(self: Self): i32; } " +
			"struct A { x: i32 } struct B { y: i32 } " +
			"impl Show for A { function show(self: Self): i32 { return self.x; } } " +
			"impl Show for B { function show(self: Self): i32 { return self.y; } } " +
			"function combine[P: Show, Q: Show](p: P, q: Q): i32 { return p.show() + q.show(); } " +
			"function main(): i32 { var a: A = A { x: 30 }; var b: B = B { y: 12 }; return combine(a, b); }", 42},
	// Parametric impl `impl[T: Bound] Trait for Box[T]` on a generic
	// struct. The impl's `for` type `Box[T]` strips to the base name
	// `Box` for the method symbol + dispatch shape compare, so a
	// `Box[Inner]` value dispatches `box.val()` to the impl method,
	// whose body calls `self.v.val()` on the struct-typed field (which
	// carries its own runtime shape, so the inner dispatch resolves
	// dynamically). Struct-typed type parameters need no
	// monomorphisation; primitive/string `T` is a follow-up (same
	// boundary bounded generics had). See docs/TRAITS.md §7a.
	{"trait-parametric-impl-struct-elem",
		"trait Valued { function val(self: Self): i32; } " +
			"struct Inner { n: i32 } " +
			"impl Valued for Inner { function val(self: Self): i32 { return self.n; } } " +
			"struct Box[T] { v: T } " +
			"impl[T: Valued] Valued for Box[T] { function val(self: Self): i32 { return self.v.val() + 1; } } " +
			"function main(): i32 { var b: Box[Inner] = Box { v: Inner { n: 41 } }; return b.val(); }", 42},
	// `dyn Trait` runtime trait object: a heterogeneous `dyn Shape[]`
	// holding two different concrete struct types dispatches each
	// element's method by runtime shape — which the self-host already
	// does for any heap value, so `dyn` only needed the type parse (the
	// `for`-loop element-type fix routes the loop var through shape
	// dispatch rather than the i32 primitive path). A `dyn Shape`
	// parameter dispatches the same way. See docs/DYN-TRAITS.md.
	{"trait-dyn-object-heterogeneous",
		"trait Shape { function area(self: Self): i32; } " +
			"struct Circle { r: i32 } struct Rect { w: i32, h: i32 } " +
			"impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } " +
			"impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } " +
			"function sum(xs: dyn Shape[]): i32 { var t: i32 = 0; for x in xs { t = t + x.area(); } return t; } " +
			"function main(): i32 { var xs: dyn Shape[] = [Circle { r: 3 }, Rect { w: 2, h: 5 }]; return sum(xs); }", 19},
	// A plain struct-array loop var calling a method — the pre-existing
	// bug the `dyn` work surfaced (the loop var defaulted to i32, so the
	// method dispatched to the primitive path). Guards the for-loop
	// element-type fix directly.
	{"trait-struct-array-loop-method",
		"struct P { v: i32 } function (p: P) get(): i32 { return p.v; } " +
			"function main(): i32 { var ps: P[] = [P { v: 3 }, P { v: 4 }]; var t: i32 = 0; for x in ps { t = t + x.get(); } return t; }", 7},
	// `@derive(Eq)` synthesises a field-wise `eq` (the same shape the Go
	// checker emits): `self.x.eq(other.x) && self.y.eq(other.y)`. The
	// field type's `.eq` is provided inline (the trait-test harness
	// doesn't load core/cmp), so this stays self-contained. r=3 only if
	// eq on equal values is true AND eq on differing values is false.
	{"trait-derive-struct-eq",
		"trait Eq { function eq(self: Self, other: Self): boolean; } " +
			"impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } " +
			"@derive(Eq) struct P { x: i32, y: i32 } " +
			"function main(): i32 { var a: P = P { x: 1, y: 2 }; var b: P = P { x: 1, y: 2 }; var c: P = P { x: 1, y: 9 }; " +
			"var r: i32 = 0; if (a.eq(b)) { r = r + 1; } if (!a.eq(c)) { r = r + 2; } return r; }", 3},
	// `@derive(Ord)` synthesises a lexicographic `cmp` — first differing
	// field decides. Inline `impl Ord for i32` provides the field cmp.
	{"trait-derive-struct-ord",
		"trait Ord { function cmp(self: Self, other: Self): i32; } " +
			"impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } } " +
			"@derive(Ord) struct P { x: i32, y: i32 } " +
			"function main(): i32 { var a: P = P { x: 1, y: 2 }; var c: P = P { x: 1, y: 9 }; " +
			"if (a.cmp(c) < 0) { if (c.cmp(a) > 0) { if (a.cmp(a) == 0) { return 42; } } } return 0; }", 42},
	// `@derive(Display)` renders the same `Name { f: v, … }` string the
	// Go checker emits, recursing into a nested derived struct. The i32
	// + string `.to_string()` are emitter intrinsics, so no stdlib
	// import is needed. Returns the rendered length (oracle-matched).
	{"trait-derive-struct-display-nested",
		"@derive(Display) struct Inner { n: i32 } " +
			"@derive(Display) struct Outer { a: Inner, tag: string } " +
			"function main(): i32 { var p: Outer = Outer { a: Inner { n: 5 }, tag: \"hi\" }; return p.to_string().len(); }",
		len("Outer { a: Inner { n: 5 }, tag: hi }")},
	// Enum RECEIVER method — the dispatch fix. A method registered on
	// the enum type is found for a variant value (whose runtime shape is
	// the variant) via the enum-method fallback; its `match (self)`
	// dispatches on the variant. Circle(3)→27, Square(4)→16, sum 43.
	{"trait-enum-method",
		"enum Shape { Circle(i32), Square(i32) } " +
			"function (s: Shape) area(): i32 { match (s) { Circle(r) => { return r * r * 3; }, Square(w) => { return w * w; } } } " +
			"function main(): i32 { var a: Shape = Circle(3); var b: Shape = Square(4); return a.area() + b.area(); }", 43},
	// Enum-receiver method on an UNANNOTATED enum local (`var a = Circle(3)`,
	// `var c = Green`) — the slot is typed from the variant's enum_owner so the
	// `<Enum>.<method>` dispatch resolves. Regression guard: the struct-array-
	// literal `else if (struct_ty == "")` clause used to swallow the enum-init
	// typing for a non-array init, silently routing these to AST.
	{"trait-enum-method-unannot-local",
		"enum Shape { Circle(i32), Square(i32) } " +
			"function (s: Shape) area(): i32 { match (s) { Circle(r) => { return r * r * 3; }, Square(w) => { return w * w; } } } " +
			"function main(): i32 { var a = Circle(3); var b = Square(4); return a.area() + b.area(); }", 43},
	{"trait-enum-method-unannot-payloadless",
		"enum Color { Red, Green } " +
			"function (c: Color) code(): i32 { match (c) { Red => { return 1; }, Green => { return 2; } } } " +
			"function main(): i32 { var c = Green; var d = Red; return c.code() * 10 + d.code(); }", 21},
	// `@derive(Display)` on an enum: variant-wise `Variant(payload)` /
	// `Variant`. `Has(7)`→"Has(7)" (6), `Nil`→"Nil" (3); 6+3=9.
	{"trait-derive-enum-display",
		"@derive(Display) enum Opt { Has(i32), Nil } " +
			"function main(): i32 { var h: Opt = Has(7); var n: Opt = Nil; return h.to_string().len() + n.to_string().len(); }",
		len("Has(7)") + len("Nil")},
	// `@derive(Eq)` on an enum: same variant compares payloads, any
	// other variant is unequal. Inline `impl Eq for i32` for the payload
	// (the trait-test harness doesn't load core/cmp). r=15.
	{"trait-derive-enum-eq",
		"trait Eq { function eq(self: Self, other: Self): boolean; } " +
			"impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } " +
			"@derive(Eq) enum Opt { Has(i32), Nil } " +
			"function main(): i32 { var r: i32 = 0; " +
			"if (Has(5).eq(Has(5))) { r = r + 1; } if (!Has(5).eq(Has(6))) { r = r + 2; } " +
			"if (!Has(5).eq(Nil)) { r = r + 4; } if (Nil.eq(Nil)) { r = r + 8; } return r; }", 15},
	// `@derive(Ord)` on an enum: a variant declared earlier sorts before
	// a later one; within a variant the payload decides. Inline
	// `impl Ord for i32` for the payload cmp. r=15.
	{"trait-derive-enum-ord",
		"trait Ord { function cmp(self: Self, other: Self): i32; } " +
			"impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } } " +
			"@derive(Ord) enum Lvl { Low(i32), High } " +
			"function main(): i32 { var r: i32 = 0; " +
			"if (Low(1).cmp(Low(2)) < 0) { r = r + 1; } if (Low(9).cmp(High) < 0) { r = r + 2; } " +
			"if (High.cmp(Low(0)) > 0) { r = r + 4; } if (Low(3).cmp(Low(3)) == 0) { r = r + 8; } return r; }", 15},
	// `@derive(Json)` on a struct synthesises a field-wise `to_json`
	// rendering the canonical JSON object `{"f":<f.to_json()>,…}` — the
	// same shape the Go checker's synthJson emits, composing through each
	// field's own `Json` impl (i32 → number, string → quoted). The inline
	// `trait Json` + primitive impls keep the case self-contained (the
	// trait-test harness doesn't load std/json). Returns the rendered
	// length: `{"id":7,"tag":"hi"}` (oracle-matched). The to_json body is
	// structurally identical to the Display `to_string` body (string
	// concat + per-field dispatch), so it lowers through the same IR path.
	{"trait-derive-struct-json",
		"trait Json { function to_json(self: Self): string; } " +
			"impl Json for i32 { function to_json(self: Self): string { return self.to_string(); } } " +
			"impl Json for string { function to_json(self: Self): string { return \"\\\"\" + self + \"\\\"\"; } } " +
			"@derive(Json) struct Item { id: i32, tag: string } " +
			"function main(): i32 { var p: Item = Item { id: 7, tag: \"hi\" }; return p.to_json().len(); }",
		len(`{"id":7,"tag":"hi"}`)},
	// `@derive(Json)` on an enum: externally-tagged — a unit variant
	// renders as its quoted name (`"Nil"`), a single-payload variant as a
	// one-key object (`{"Has":<__p0.to_json()>}`). Mirrors the Go
	// synthEnumJson (self-host enum variants carry at most one payload).
	// `Has(7)`→`{"Has":7}` (9) + `Nil`→`"Nil"` (5) = 14.
	{"trait-derive-enum-json",
		"trait Json { function to_json(self: Self): string; } " +
			"impl Json for i32 { function to_json(self: Self): string { return self.to_string(); } } " +
			"@derive(Json) enum Opt { Has(i32), Nil } " +
			"function main(): i32 { var h: Opt = Has(7); var n: Opt = Nil; return h.to_json().len() + n.to_json().len(); }",
		len(`{"Has":7}`) + len(`"Nil"`)},
	// `@derive(Debug)` on a struct synthesises a `to_debug` rendering the
	// structural `Name { f: <debug f>, … }` form — the Debug sibling of
	// the derived Display. The one difference is that a string field
	// renders QUOTED (`tag: "hi"`), so a debug dump is unambiguous; i32 +
	// string render via emitter intrinsics, so no trait/impl is needed.
	// `Item { id: 7, tag: "hi" }` (oracle-matched length).
	{"trait-derive-struct-debug",
		"@derive(Debug) struct Item { id: i32, tag: string } " +
			"function main(): i32 { var p: Item = Item { id: 7, tag: \"hi\" }; return p.to_debug().len(); }",
		len(`Item { id: 7, tag: "hi" }`)},
	// `@derive(Debug)` on an enum: `Variant` / `Variant(<debug payload>)`.
	// A string payload renders quoted (`Word("hi")`), the Debug vs Display
	// distinction. `Word("hi")`→10 + `End`→3 = 13.
	{"trait-derive-enum-debug",
		"@derive(Debug) enum Msg { Word(string), End } " +
			"function main(): i32 { var w: Msg = Word(\"hi\"); var e: Msg = End; return w.to_debug().len() + e.to_debug().len(); }",
		len(`Word("hi")`) + len("End")},
	// `@derive(Hash)` on a struct synthesises a field-wise fold `h = h*31 +
	// self.f.hash()` from a seed of 17, composing through each field's own
	// `Hash` impl (inline `impl Hash for i32` provides the field dispatch;
	// the trait-test harness doesn't load core/cmp). Paired with Eq under
	// `a == b ⇒ a.hash() == b.hash()`. r=3 only if equal values hash the
	// same AND differing values hash apart.
	{"trait-derive-struct-hash",
		"trait Hash { function hash(self: Self): i32; } " +
			"impl Hash for i32 { function hash(self: Self): i32 { return self; } } " +
			"@derive(Hash) struct P { x: i32, y: i32 } " +
			"function main(): i32 { var a: P = P { x: 3, y: 4 }; var b: P = P { x: 3, y: 4 }; var c: P = P { x: 5, y: 4 }; " +
			"var r: i32 = 0; if (a.hash() == b.hash()) { r = r + 1; } if (a.hash() != c.hash()) { r = r + 2; } return r; }", 3},
	// `@derive(Hash)` on an enum: the accumulator is seeded with the
	// variant tag (17 + declaration index) so distinct variants hash
	// apart, then the single payload is folded in. Inline `impl Hash for
	// i32` for the payload. r=3 only if distinct variants differ AND the
	// same variant+payload hashes equal.
	{"trait-derive-enum-hash",
		"trait Hash { function hash(self: Self): i32; } " +
			"impl Hash for i32 { function hash(self: Self): i32 { return self; } } " +
			"@derive(Hash) enum Shape { Dot, Circle(i32) } " +
			"function main(): i32 { var r: i32 = 0; " +
			"if (Dot.hash() != Circle(0).hash()) { r = r + 1; } if (Circle(7).hash() == Circle(7).hash()) { r = r + 2; } return r; }", 3},
	// Generic-struct monomorphisation: a `@derive(Display) struct Box[T]`
	// instantiated at `Box[i32]` is cloned to a concrete `Box__i32` with
	// `v: i32`, so `self.v.to_string()` dispatches statically to the i32
	// helper (the erased "T" shape couldn't). Renders "Box { v: 5 }" (12).
	{"trait-generic-struct-derive-display-i32",
		"@derive(Display) struct Box[T] { v: T } " +
			"function main(): i32 { var b: Box[i32] = Box { v: 5 }; return b.to_string().len(); }",
		len("Box { v: 5 }")},
	// Same generic struct instantiated at `Box[string]` — a SEPARATE
	// `Box__string` clone whose `self.v` is a string. Renders
	// "Box { v: hi }" (13).
	{"trait-generic-struct-derive-display-string",
		"@derive(Display) struct Box[T] { v: T } " +
			"function main(): i32 { var b: Box[string] = Box { v: \"hi\" }; return b.to_string().len(); }",
		len("Box { v: hi }")},
	// Both instantiations of the same generic struct coexisting: two
	// clones (`Box__i32`, `Box__string`) each dispatch `to_string` to its
	// own concrete field type. 12 + 13 = 25. Guards the shared-field-name
	// dispatch (both clones declare `v`).
	{"trait-generic-struct-derive-display-both",
		"@derive(Display) struct Box[T] { v: T } " +
			"function main(): i32 { var a: Box[i32] = Box { v: 5 }; var b: Box[string] = Box { v: \"hi\" }; " +
			"return a.to_string().len() + b.to_string().len(); }",
		len("Box { v: 5 }") + len("Box { v: hi }")},
	// `@derive(Eq)` on a generic struct: the cloned `Box__i32` gets a
	// field-wise `eq` whose `self.v.eq(other.v)` dispatches to the inline
	// `impl Eq for i32`. r=3 only if equal values compare true AND
	// differing values compare false.
	{"trait-generic-struct-derive-eq",
		"trait Eq { function eq(self: Self, other: Self): boolean; } " +
			"impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } " +
			"@derive(Eq) struct Box[T] { v: T } " +
			"function main(): i32 { var a: Box[i32] = Box { v: 5 }; var b: Box[i32] = Box { v: 5 }; var c: Box[i32] = Box { v: 9 }; " +
			"var r: i32 = 0; if (a.eq(b)) { r = r + 1; } if (!a.eq(c)) { r = r + 2; } return r; }", 3},
	// `@derive(Ord)` on a generic struct: the cloned `cmp` compares the
	// concrete `i32` field via the inline `impl Ord for i32`. 42 only if
	// the lexicographic cmp orders correctly in both directions + equal.
	{"trait-generic-struct-derive-ord",
		"trait Ord { function cmp(self: Self, other: Self): i32; } " +
			"impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } } " +
			"@derive(Ord) struct Box[T] { v: T } " +
			"function main(): i32 { var a: Box[i32] = Box { v: 1 }; var c: Box[i32] = Box { v: 9 }; " +
			"if (a.cmp(c) < 0) { if (c.cmp(a) > 0) { if (a.cmp(a) == 0) { return 42; } } } return 0; }", 42},
	// A parametric `impl[T: Show] Show for Box[T]` cloned per concrete T:
	// `Box__i32`'s method dispatches `self.v.show()` to `impl Show for
	// i32`, `Box__string`'s to `impl Show for string`. "Box(7)"=6 +
	// "Box(hi)"=7 = 13. Exercises generic receiver-method monomorphisation
	// over a primitive AND a string T in one program.
	{"trait-generic-struct-parametric-impl",
		"trait Show { function show(self: Self): string; } " +
			"impl Show for i32 { function show(self: Self): string { return self.to_string(); } } " +
			"impl Show for string { function show(self: Self): string { return self; } } " +
			"struct Box[T] { v: T } " +
			"impl[T: Show] Show for Box[T] { function show(self: Self): string { return \"Box(\" + self.v.show() + \")\"; } } " +
			"function main(): i32 { var a: Box[i32] = Box { v: 7 }; var b: Box[string] = Box { v: \"hi\" }; " +
			"return a.show().len() + b.show().len(); }",
		len("Box(7)") + len("Box(hi)")},
}

// TestSelfHostTraitsX86_64 — trait/impl support with the self-hosted
// x86-64 compiler. Trait parsing lives entirely in the shared lexer +
// parser, so the asm emitter needed no change.
func TestSelfHostTraitsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range traitsCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostTraitsArm64 — CI-gated arm64 counterpart. Trait support
// lives entirely in the shared parser, so the arm64 emitter needed no
// change; this guards that the shared path stays sound on arm64.
func TestSelfHostTraitsArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range traitsCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
