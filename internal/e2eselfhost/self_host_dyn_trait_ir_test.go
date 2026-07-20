package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dynTraitIRCases exercise `dyn Trait` method dispatch through the stack-IR
// path. A `dyn Trait` value's concrete type is known only at runtime, so
// `x.method()` is a DYNAMIC dispatch: the receiver (+ args) are spilled into
// temp locals and op_dyn_dispatch reads the receiver's runtime shape pointer,
// dispatching to the matching `<ConcreteType>.<method>` via a compare-branch
// chain over the trait's impl types (the same shape `match`/`variant_is`
// reads). Exit codes are the oracle.
var dynTraitIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// The motivating case: a heterogeneous `dyn Shape[]` iterated in a loop.
	// 3*3 + 2*5 = 9 + 10 = 19.
	{"heterogeneous-array",
		`trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function sum(xs: dyn Shape[]): i32 { var t: i32 = 0; for x in xs { t = t + x.area(); } return t; } function main(): i32 { var xs: dyn Shape[] = [Circle { r: 3 }, Rect { w: 2, h: 5 }]; return sum(xs); }`, 19},
	// A `dyn Shape` SCALAR param, receiving a Circle. 4*4 = 16.
	{"param-circle",
		`trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function ar(s: dyn Shape): i32 { return s.area(); } function main(): i32 { var c: Circle = Circle { r: 4 }; return ar(c); }`, 16},
	// Same param, receiving a Rect — the OTHER impl arm. 2*5 = 10.
	{"param-rect",
		`trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function ar(s: dyn Shape): i32 { return s.area(); } function main(): i32 { var r: Rect = Rect { w: 2, h: 5 }; return ar(r); }`, 10},
	// A trait method taking an ARGUMENT, dispatched dynamically. 5 * 3 = 15.
	{"method-with-arg",
		`trait Sc { function sc(self: Self, k: i32): i32; } struct A { v: i32 } struct B { v: i32 } impl Sc for A { function sc(self: Self, k: i32): i32 { return self.v * k; } } impl Sc for B { function sc(self: Self, k: i32): i32 { return self.v + k; } } function f(s: dyn Sc): i32 { return s.sc(3); } function main(): i32 { var a: A = A { v: 5 }; return f(a); }`, 15},
	// Three impls in a heterogeneous array — exercises a longer compare-branch
	// chain. 3*3 + 2*5 + 7 = 9 + 10 + 7 = 26.
	{"three-impls",
		`trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } struct Unit { } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } impl Shape for Unit { function area(self: Self): i32 { return 7; } } function sum(xs: dyn Shape[]): i32 { var t: i32 = 0; for x in xs { t = t + x.area(); } return t; } function main(): i32 { var xs: dyn Shape[] = [Circle { r: 3 }, Rect { w: 2, h: 5 }, Unit { }]; return sum(xs); }`, 26},

	// --- `dyn` over PRIMITIVE / string receivers (docs/DYN-TRAITS.md §4.2.3) ---
	// A primitive value has no shape pointer, so it is heap-boxed at the coercion
	// site into a `dyn` cell [shape@0, value@8] (op_dyn_box); dispatch matches the
	// box's offset-0 shape (the interned primitive type name / id) and UNBOXES the
	// value from offset 8 before calling `<prim>.<method>`.

	// `dyn` over i32, SCALAR param. The arg 41 is boxed at the call; show() adds 1.
	{"prim-i32-scalar",
		`trait Show { function show(self: Self): i32; } impl Show for i32 { function show(self: Self): i32 { return self + 1; } } function run(s: dyn Show): i32 { return s.show(); } function main(): i32 { var x: i32 = 41; return run(x); }`, 42},
	// `dyn` over i32 with a method ARGUMENT. 5 * 3 = 15 (the unboxed receiver 5,
	// the plain arg 3).
	{"prim-i32-method-arg",
		`trait Sc { function sc(self: Self, k: i32): i32; } impl Sc for i32 { function sc(self: Self, k: i32): i32 { return self * k; } } function f(s: dyn Sc): i32 { return s.sc(3); } function main(): i32 { var a: i32 = 5; return f(a); }`, 15},
	// `dyn` over `string`: the value is a one-word string-box pointer, boxed like
	// any primitive. show() returns its length. len("hello") = 5.
	{"prim-string",
		`trait Show { function show(self: Self): i32; } impl Show for string { function show(self: Self): i32 { return self.len(); } } function run(s: dyn Show): i32 { return s.show(); } function main(): i32 { var x: string = "hello"; return run(x); }`, 5},
	// A homogeneous `dyn`-over-i32 ARRAY: each element is boxed at the array
	// literal, then iterated + dispatched through runtime shape. 3 + 4 + 5 = 12.
	{"prim-i32-array",
		`trait Show { function show(self: Self): i32; } impl Show for i32 { function show(self: Self): i32 { return self; } } function sum(xs: dyn Show[]): i32 { var t: i32 = 0; for x in xs { t = t + x.show(); } return t; } function main(): i32 { var xs: dyn Show[] = [3, 4, 5]; return sum(xs); }`, 12},

	// --- SCALAR `dyn` coercion at var-init / assignment / return (§4.3) ---
	// `var d: dyn Show = <i32>` — the primitive init is heap-boxed at the
	// binding (op_dyn_box); `d.show()` dispatches through the boxed shape and
	// unboxes the receiver. show() adds 1: 41 + 1 = 42.
	{"prim-i32-var-init",
		`trait Show { function show(self: Self): i32; } impl Show for i32 { function show(self: Self): i32 { return self + 1; } } function main(): i32 { var d: dyn Show = 41; return d.show(); }`, 42},
	// `var d: dyn Show = <Circle>` — a STRUCT init flows UNBOXED, and the slot's
	// type is "dyn Show" (NOT "Circle") so `d.show()` dispatches DYNAMICALLY
	// (regression: must not static-dispatch to Circle.show). 4*4 = 16.
	{"struct-var-init",
		`trait Show { function show(self: Self): i32; } struct Circle { r: i32 } impl Show for Circle { function show(self: Self): i32 { return self.r * self.r; } } function main(): i32 { var d: dyn Show = Circle { r: 4 }; return d.show(); }`, 16},
	// `d = <i32>` — reassigning a SCALAR dyn local boxes the primitive RHS at
	// the assignment. Init with 10 (show -> 11), then reassign 41 (show -> 42).
	{"prim-i32-assign",
		`trait Show { function show(self: Self): i32; } impl Show for i32 { function show(self: Self): i32 { return self + 1; } } function main(): i32 { var d: dyn Show = 10; d = 41; return d.show(); }`, 42},
	// `return <i32>` from a SCALAR `dyn Show`-returning function: the primitive
	// is boxed at the return site; the caller binds the box as a dyn local and
	// dispatches. 5 -> show() returns 5: pick().show() = 5.
	{"prim-i32-return",
		`trait Show { function show(self: Self): i32; } impl Show for i32 { function show(self: Self): i32 { return self; } } function pick(): dyn Show { return 5; } function main(): i32 { var d: dyn Show = pick(); return d.show(); }`, 5},

	// --- `e as? T` downcast (docs/DYN-TRAITS.md §9) ---
	// The downcast reads the dyn value's offset-0 shape and compares it to T's
	// interned shape: Some(v) on an exact match (v IS the concrete T pointer),
	// else None. The result is an ordinary Option a `match` reads.

	// HIT: a `dyn Shape` holding a Circle, `s as? Circle` → Some(circle); the
	// bound value is usable as a Circle (field `r`). r = 7.
	{"downcast-hit",
		`trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function dc(s: dyn Shape): i32 { match (s as? Circle) { Some(c) => { return c.r; }, None => { return 0; } } } function main(): i32 { var c: Circle = Circle { r: 7 }; return dc(c); }`, 7},
	// MISS: a `dyn Shape` holding a Rect, `s as? Circle` → None → 0.
	{"downcast-miss",
		`trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function dc(s: dyn Shape): i32 { match (s as? Circle) { Some(c) => { return c.r; }, None => { return 0; } } } function main(): i32 { var r: Rect = Rect { w: 2, h: 5 }; return dc(r); }`, 0},
	// The OTHER target on the same value: a `dyn Shape` Rect, `s as? Rect` → Some;
	// w*h = 2*5 = 10.
	{"downcast-hit-rect",
		`trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function dc(s: dyn Shape): i32 { match (s as? Rect) { Some(re) => { return re.w * re.h; }, None => { return 0; } } } function main(): i32 { var r: Rect = Rect { w: 2, h: 5 }; return dc(r); }`, 10},
	// HETEROGENEOUS `dyn Shape[]`: downcast each element to Circle and count the
	// hits. [Circle, Rect, Circle] → 2 circles.
	{"downcast-array-count",
		`trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function count(xs: dyn Shape[]): i32 { var n: i32 = 0; for x in xs { match (x as? Circle) { Some(c) => { n = n + 1; }, None => { } } } return n; } function main(): i32 { var xs: dyn Shape[] = [Circle { r: 3 }, Rect { w: 2, h: 5 }, Circle { r: 1 }]; return count(xs); }`, 2},

	// --- MULTI-TRAIT `dyn A + B` downcast (docs/DYN-TRAITS.md §10). The
	// self-host downcast is SHAPE-based (op_dyn_downcast compares the dyn
	// value's runtime shape to T's interned shape) — it never looks at the
	// trait set, so a multi-trait `dyn A + B` value (a heap pointer with a
	// shape, exactly like a single-trait one) downcasts for free. These pin
	// that it parses + lowers + runs.
	// HIT: a `dyn Show + Weigh` holding an Apple, `d as? Apple` → Some; g = 7.
	{"downcast-multi-hit",
		`trait Show { function show(self: Self): i32; } trait Weigh { function weight(self: Self): i32; } struct Apple { g: i32 } struct Brick { kg: i32 } impl Show for Apple { function show(self: Self): i32 { return self.g; } } impl Weigh for Apple { function weight(self: Self): i32 { return self.g; } } impl Show for Brick { function show(self: Self): i32 { return self.kg; } } impl Weigh for Brick { function weight(self: Self): i32 { return self.kg; } } function dc(d: dyn Show + Weigh): i32 { match (d as? Apple) { Some(a) => { return a.g; }, None => { return 0; } } } function main(): i32 { var x: Apple = Apple { g: 7 }; return dc(x); }`, 7},
	// MISS: a `dyn Show + Weigh` holding a Brick, `d as? Apple` → None → 0.
	{"downcast-multi-miss",
		`trait Show { function show(self: Self): i32; } trait Weigh { function weight(self: Self): i32; } struct Apple { g: i32 } struct Brick { kg: i32 } impl Show for Apple { function show(self: Self): i32 { return self.g; } } impl Weigh for Apple { function weight(self: Self): i32 { return self.g; } } impl Show for Brick { function show(self: Self): i32 { return self.kg; } } impl Weigh for Brick { function weight(self: Self): i32 { return self.kg; } } function dc(d: dyn Show + Weigh): i32 { match (d as? Apple) { Some(a) => { return a.g; }, None => { return 99; } } } function main(): i32 { var x: Brick = Brick { kg: 3 }; return dc(x); }`, 99},

	// --- STRING-returning `dyn Trait` methods, chained (#5142). The dispatch
	// returns the string in the result register; a string method chained
	// DIRECTLY on it must see a string receiver so `.len()` lowers as the
	// length read ([ptr-4], the L2 header) rather than a plain deref ([ptr+0],
	// the string data). Before the fix the dyn-dispatched method's declared
	// return type wasn't tracked onto the result, so `d.name().len()` read the
	// data pointer as a length and returned garbage (0). The trait's required
	// method signature carries the return type; a "dyn <Trait>.<method>" entry
	// in str_ret_fns makes the chained lowering track the string.
	// `d.name().len()` on an SSO-inline string ("hello", <=7 bytes) → 5.
	{"dyn-string-len-chained",
		`trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function main(): i32 { var p: P = P { tag: "hello" }; var d: dyn Named = p; return d.name().len(); }`, 5},
	// Same on a HEAP string (>7 bytes, out-of-line data) → 27.
	{"dyn-string-len-heap",
		`trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function main(): i32 { var p: P = P { tag: "this is a long string value" }; var d: dyn Named = p; return d.name().len(); }`, 27},
	// Materialising into a `var s: string` first was the workaround — pin that
	// it still works (the slot type already carried the string). → 5.
	{"dyn-string-len-via-var",
		`trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function main(): i32 { var p: P = P { tag: "hello" }; var d: dyn Named = p; var s: string = d.name(); return s.len(); }`, 5},
	// Concat chained on a dyn string result: (d.name() + "cd").len() = 2 + 2 = 4.
	{"dyn-string-concat-chained",
		`trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function main(): i32 { var p: P = P { tag: "ab" }; var d: dyn Named = p; return (d.name() + "cd").len(); }`, 4},
	// A `dyn Named` PARAM, chained `.len()` inside the callee. len("hiya") = 4.
	{"dyn-string-len-param",
		`trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function f(d: dyn Named): i32 { return d.name().len(); } function main(): i32 { var p: P = P { tag: "hiya" }; return f(p); }`, 4},
	// Precision guard (#5151, root-caused + fixed by #5149): a `dyn Foo` whose
	// method `bar` returns i32, alongside an UNRELATED inherent `S.bar()` that
	// returns a string. expr_is_str's dyn-receiver arm used to scan str_ret_fns
	// by BARE method name, matching the unrelated `S.bar`, so `d.bar()` was
	// typed a string and `d.bar() + sv.bar().len()` mis-lowered to
	// `__fern_strcat` over the i32 result — garbage/trap on wasm (= 1) and, on
	// x86, strcat walking the integer as a pointer until it faulted (the CI
	// "26 s hang → exit -1", layout-sensitive because the walk length depended
	// on heap contents). The mis-lowering was deterministic in the shared
	// irlower, not an uninitialized read; #5149's exact-qualified
	// "dyn <Trait>.<method>" lookup eliminates it on every backend. 9 + 3 = 12.
	{"dyn-i32-vs-unrelated-string",
		`trait Foo { function bar(self: Self): i32; } struct A { n: i32 } impl Foo for A { function bar(self: Self): i32 { return self.n; } } struct S { s: string } impl S { function bar(self: Self): string { return self.s; } } function main(): i32 { var a: A = A { n: 9 }; var d: dyn Foo = a; var sv: S = S { s: "xyz" }; return d.bar() + sv.bar().len(); }`, 12},

	// --- NUMERIC-returning `dyn Trait` methods, chained in arithmetic. Same
	// "result type not tracked onto the dispatch result" class as the string
	// cases above: a 64-bit / float value returned by the dispatch and chained
	// (`d.v() + 1`) width-tracked as i32 (i64/u64) or as integer bits (f64/f32),
	// so the arithmetic mis-lowered. "dyn <Trait>.<method>" entries in
	// i64_ret_fns / f64_ret_fns fix each; the `if (r == …) 1 else 0` returns 1
	// only when the arithmetic is done at the correct width.
	// i64: 5_000_000_000 + 1 == 5_000_000_001 (truncates to garbage at i32).
	{"dyn-i64-chained",
		`trait Big { function v(self: Self): i64; } struct P { n: i64 } impl Big for P { function v(self: Self): i64 { return self.n; } } function main(): i32 { var p: P = P { n: 5000000000 }; var d: dyn Big = p; var r: i64 = d.v() + 1; if (r == 5000000001) { return 1; } return 0; }`, 1},
	// u64 rides the i64 path — same fix.
	{"dyn-u64-chained",
		`trait U { function v(self: Self): u64; } struct P { n: u64 } impl U for P { function v(self: Self): u64 { return self.n; } } function main(): i32 { var p: P = P { n: 5000000000 }; var d: dyn U = p; var r: u64 = d.v() + 1; if (r == 5000000001) { return 1; } return 0; }`, 1},
	// f64: 2.5 + 0.5 == 3.0 (integer add on the float bits gives a wrong value).
	{"dyn-f64-chained",
		`trait Fl { function f(self: Self): f64; } struct S { v: f64 } impl Fl for S { function f(self: Self): f64 { return self.v; } } function main(): i32 { var s: S = S { v: 2.5 }; var d: dyn Fl = s; var r: f64 = d.f() + 0.5; if (r == 3.0) { return 1; } return 0; }`, 1},
	// f32 rides the f64 twin for value ops — same fix.
	{"dyn-f32-chained",
		`trait F { function v(self: Self): f32; } struct P { n: f32 } impl F for P { function v(self: Self): f32 { return self.n; } } function main(): i32 { var p: P = P { n: 2.5 }; var d: dyn F = p; var r: f32 = d.v() + 0.5; if (r == 3.0) { return 1; } return 0; }`, 1},
	// Regression guard for a non-scalar return that DOES track + route IR: an
	// array `.len()` on the dispatch result. (A struct-field or Option-match on a
	// dyn dispatch result currently routes to the AST fallback — a legit
	// IR-subset gap, not a miscompile — so those shapes aren't pinned here.)
	{"dyn-array-len-chained",
		`trait Arr { function make(self: Self): i32[]; } struct S { n: i32 } impl Arr for S { function make(self: Self): i32[] { return [self.n, self.n, self.n]; } } function main(): i32 { var s: S = S { n: 1 }; var d: dyn Arr = s; return d.make().len(); }`, 3},

	// --- DIRECTLY-INDEXED dyn-array dispatch results (`d.make()[i]`, no
	// explicitly-typed intermediate `var xs: T[]`). The element WIDTH/type of the
	// dispatch result must be tracked via the array registries keyed
	// "dyn <Trait>.<method>" — without it a directly-indexed f64 element reads at
	// the wrong width and a string element mis-lowers its chained `.len()`.
	// (Binding through `var xs: T[] = d.make()` already types the slot, so only
	// the direct-chain shape is affected. An i64[]/u64[] direct index of a dyn
	// result routes to the AST fallback today — a legit IR-subset gap, not a
	// miscompile — so it is not pinned here and the i64arr registry gets no dyn
	// entry.)
	// f64[] element: [2.5, 3.5][1] + 1.0 == 4.5.
	{"dyn-arr-f64-direct-index",
		`trait Arr { function make(self: Self): f64[]; } struct S { v: f64 } impl Arr for S { function make(self: Self): f64[] { return [self.v, self.v + 1.0]; } } function main(): i32 { var s: S = S { v: 2.5 }; var d: dyn Arr = s; var r: f64 = d.make()[1] + 1.0; if (r == 4.5) { return 1; } return 0; }`, 1},
	// string[] element, chained `.len()`: ["ab","hello"][1].len() == 5.
	{"dyn-arr-string-direct-index",
		`trait Arr { function make(self: Self): string[]; } struct S { s: string } impl Arr for S { function make(self: Self): string[] { return [self.s, "hello"]; } } function main(): i32 { var s: S = S { s: "ab" }; var d: dyn Arr = s; return d.make()[1].len(); }`, 5},
}

// TestSelfHostDynTraitIRX86_64 routes each case through the self-hosted x86-64
// driver (asm_run) and asserts the exit code, AND probes the routing
// (asm_pathprobe_run) to pin each case to the "ir" path.
func TestSelfHostDynTraitIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range dynTraitIRCases {
		t.Run(tc.name, func(t *testing.T) {
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
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
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostDynTraitIRWasm runs the same cases through the wasm IR backend
// (wasm_ir_run -ir): the shape is a numeric type id (i32.load @0), and the
// dispatch is a nested if/else chain.
func TestSelfHostDynTraitIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host dyn-trait wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range dynTraitIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, "dyntrait_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("dyn-trait wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
