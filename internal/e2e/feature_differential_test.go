// Cross-area differential coverage: the same source run through the
// interpreter (oracle) and every codegen backend (x86-64, arm64,
// wasm), asserting they all agree. Where numeric_property_test.go
// sweeps the integer/float matrix with a generator, this file pins a
// broad table of hand-written programs across the *structural*
// surface — strings, arrays, maps, structs, enums, Option/Result +
// match, closures, and control flow — so a backend that mishandles
// one of them diverges from the interp.
//
// Reuses assertNumProgramAgrees (defined in numeric_property_test.go),
// which compares trimmed stdout across all available backends.
package e2e

import "testing"

// TestFeatureDifferential runs each program across every backend and
// asserts agreement. A divergence here is a real cross-backend bug
// (it's how the closure mixed-capture segfault below was found).
func TestFeatureDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cross-area differential in -short mode")
	}
	cases := []struct {
		name, src string
	}{
		// ---- closures ----
		// Regression: a closure capturing BOTH a string and an i32
		// duplicated its capture list in the checker, which segfaulted
		// on arm64 (mixed-width env layout) while the other backends
		// silently over-allocated. Must be 105 everywhere.
		{"closure_capture_string_and_i32", `import "std/i32";
function outer(s: string): i32 {
    var bonus: i32 = 100;
    function inner(): i32 { return s.len() + bonus; }
    return inner();
}
function main(): i32 { print(outer("hello").to_string()); return 0; }`},
		{"closure_capture_i32_then_string", `import "std/i32";
function outer(s: string): i32 {
    var a: i32 = 1;
    function inner(): i32 { return a + s.len(); }
    return inner();
}
function main(): i32 { print(outer("hi").to_string()); return 0; }`},
		{"closure_adder", `import "std/i32";
function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    var f = makeAdder(7);
    var g = makeAdder(10);
    print(f(35).to_string());
    print((f(1) + g(1)).to_string());
    return 0;
}`},
		{"closure_capture_two_strings", `import "std/i32";
function outer(s: string): i32 {
    var t: string = "world";
    function inner(): i32 { return s.len() + t.len(); }
    return inner();
}
function main(): i32 { print(outer("hello").to_string()); return 0; }`},
		// Regression: a two-word string capture landing at a
		// non-8-aligned env offset (here after a single i32) faulted
		// arm64's env access until the capture layout was 8-aligned.
		{"closure_capture_i32_string_unaligned", `import "std/i32";
function outer(s: string): i32 {
    var a: i32 = 1;
    function inner(): i32 { return a + s.len(); }
    return inner();
}
function main(): i32 { print(outer("hi").to_string()); return 0; }`},
		{"closure_capture_struct_i32_string", `import "std/i32";
struct P { x: i32, y: i32 }
function outer(): i32 {
    var p: P = P{x: 3, y: 4};
    var n: i32 = 10;
    var s: string = "hi";
    function inner(): i32 { return p.x + n + s.len(); }
    return inner();
}
function main(): i32 { print(outer().to_string()); return 0; }`},
		{"closure_capture_i64_i32_string", `import "std/i32";
import "std/i64";
function outer(): i64 {
    var a: i64 = 5000000000;
    var b: i32 = 3;
    var s: string = "z";
    function inner(): i64 { return a + b as i64 + s.len() as i64; }
    return inner();
}
function main(): i32 { print(outer().to_string()); return 0; }`},

		// ---- strings ----
		{"string_ops", `import "std/string";
import "std/i32";
function pb(x: boolean): string { if (x) { return "T"; } return "F"; }
function main(): i32 {
    var s: string = "Hello, World";
    print(s.len().to_string());
    print(s[0:5]);
    print(s + "!");
    print(s.index_of("World").to_string());
    print(pb(s.starts_with("Hello")));
    print(pb(s.contains("lo, W")));
    print(s.repeat(2));
    print("héllo".len().to_string());
    return 0;
}`},

		// ---- arrays ----
		{"array_ops", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    xs = xs.push(40);
    print(xs.len().to_string());
    print(xs[3].to_string());
    var sum: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { sum = sum + xs[i]; i = i + 1; }
    print(sum.to_string());
    var ys: string[] = ["a", "b", "c"];
    print(ys[0] + ys[2]);
    return 0;
}`},

		// ---- maps ----
		{"map_ops", `import "std/i32";
import "core/map";
function pb(x: boolean): string { if (x) { return "T"; } return "F"; }
function main(): i32 {
    var m: Map[i32, i32] = map_new(8);
    m.set(1, 100);
    m.set(2, 200);
    m.set(1, 111);
    print(m.get_or(1, 0).to_string());
    print(m.get_or(3, 0 - 1).to_string());
    print(m.len().to_string());
    print(pb(m.has(2)));
    print(pb(m.has(9)));
    return 0;
}`},

		// ---- structs ----
		{"struct_nested", `import "std/i32";
struct Point { x: i32, y: i32 }
struct Line { a: Point, b: Point }
function main(): i32 {
    var p: Point = Point{ x: 3, y: 4 };
    print((p.x + p.y).to_string());
    var l: Line = Line{ a: Point{x:1,y:2}, b: Point{x:5,y:6} };
    print((l.a.x + l.b.y).to_string());
    var p2: Point = Point{ ...p, x: 100 };
    print((p2.x + p2.y).to_string());
    return 0;
}`},

		// ---- enums + match ----
		{"enum_match", `import "std/i32";
enum Shape { Circle(i32), Rect(i32, i32), Dot }
function area(s: Shape): i32 {
    match (s) {
        Circle(r) => { return 3 * r * r; },
        Rect(w, h) => { return w * h; },
        Dot => { return 0; }
    }
}
function main(): i32 {
    print(area(Circle(5)).to_string());
    print(area(Rect(4, 6)).to_string());
    print(area(Dot).to_string());
    return 0;
}`},

		// ---- Option / Result ----
		{"option_result", `import "std/i32";
function half(x: i32): Option[i32] { if (x % 2 == 0) { return Some(x / 2); } return None; }
function check(x: i32): Result[i32, string] { if (x > 0) { return Ok(x); } return Err("neg"); }
function main(): i32 {
    match (half(10)) { Some(v) => { print(v.to_string()); }, None => { print("odd"); } }
    match (half(7)) { Some(v) => { print(v.to_string()); }, None => { print("odd"); } }
    match (check(5)) { Ok(v) => { print(v.to_string()); }, Err(e) => { print(e); } }
    match (check(0 - 3)) { Ok(v) => { print(v.to_string()); }, Err(e) => { print(e); } }
    return 0;
}`},

		// ---- control flow returning values ----
		{"control_flow", `import "std/i32";
function classify(n: i32): string {
    var label: string = if (n < 0) { "neg" } else if (n == 0) { "zero" } else { "pos" };
    return label;
}
function fib(n: i32): i32 {
    if (n < 2) { return n; }
    return fib(n - 1) + fib(n - 2);
}
function main(): i32 {
    print(classify(0 - 5));
    print(classify(0));
    print(classify(42));
    print(fib(10).to_string());
    var total: i32 = 0;
    for x in [1, 2, 3, 4, 5] { total = total + x; }
    print(total.to_string());
    return 0;
}`},

		// ---- tuples ----
		{"tuples", `import "std/i32";
function swap(p: (i32, string)): (string, i32) { return (p.1, p.0); }
function main(): i32 {
    var t: (i32, string) = (42, "hi");
    print(t.0.to_string());
    print(t.1);
    var s = swap(t);
    print(s.0);
    print(s.1.to_string());
    return 0;
}`},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			assertNumProgramAgrees(t, c.src)
		})
	}
}
