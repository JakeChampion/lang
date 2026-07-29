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
    xs = xs.append(40);
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
    m = m.insert(1, 100);
    m = m.insert(2, 200);
    m = m.insert(1, 111);
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
		// `for x in b.items { … }` — iterating a struct's array field. The
		// field-access iter must NOT be mis-parsed as a `b.items { … }`
		// qualified struct literal (the trailing `{` opens the loop body).
		{"struct_field_array_foreach", `import "std/i32";
struct Bag { items: i32[] }
function main(): i32 {
    var b: Bag = Bag{ items: [10, 20, 30] };
    var sum: i32 = 0;
    for x in b.items { sum = sum + x; }
    print(sum.to_string());
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

		// ---- generics / monomorphisation ----
		// Includes a generic over a pointer-element array (struct[]):
		// the array literal's ElemType used to keep the unsubstituted
		// type parameter, building single-word stores into the
		// pointer-width slots and corrupting the array.
		{"generics", `import "std/i32";
struct Box { v: i32 }
function identity[T](x: T): T { return x; }
function snd[A, B](a: A, b: B): B { return b; }
function len_of[T](xs: T[]): i32 { return xs.len(); }
function main(): i32 {
    print(identity(42).to_string());
    print(identity("hi"));
    print(snd(1, "x"));
    print(snd("y", 99).to_string());
    print(len_of([1, 2, 3, 4]).to_string());
    print(len_of([Box{v: 1}, Box{v: 2}, Box{v: 3}]).to_string());
    print(len_of(["a", "b"]).to_string());
    return 0;
}`},

		// ---- transitive generics (generic body calls another generic) ----
		{"transitive_generics", `import "std/i32";
function id[T](x: T): T { return x; }
function wrap[T](x: T): T { return id(x); }
function twice[T](x: T): T[] { return [id(x), id(x)]; }
function a[T](x: T): T { return wrap(x); }
function b[T](x: T): T { return a(x); }
function main(): i32 {
    print(wrap(5).to_string());
    print(wrap("hi"));
    print(b(42).to_string());
    print(b("deep"));
    var xs = twice("z");
    print(xs.len().to_string());
    print(xs[1]);
    return 0;
}`},

		// ---- recursive enum (binary tree) ----
		{"recursive_enum_tree", `import "std/i32";
enum Tree { Leaf(i32), Node(Tree, Tree) }
function sum(t: Tree): i32 {
    match (t) { Leaf(v) => { return v; }, Node(l, r) => { return sum(l) + sum(r); } }
}
function depth(t: Tree): i32 {
    match (t) {
        Leaf(v) => { return 1; },
        Node(l, r) => { var dl = depth(l); var dr = depth(r); if (dl > dr) { return dl + 1; } return dr + 1; }
    }
}
function main(): i32 {
    var t: Tree = Node(Node(Leaf(1), Leaf(2)), Leaf(3));
    print(sum(t).to_string());
    print(depth(t).to_string());
    return 0;
}`},

		// ---- struct arrays with string fields ----
		{"struct_array_string_field", `import "std/i32";
struct P { x: i32, name: string }
function main(): i32 {
    var ps: P[] = [P{x: 1, name: "a"}, P{x: 2, name: "b"}, P{x: 3, name: "c"}];
    var i: i32 = 0;
    var out: string = "";
    while (i < ps.len()) { out = out + ps[i].name + ps[i].x.to_string(); i = i + 1; }
    print(out);
    return 0;
}`},

		// ---- nested closures (closure returning closure) ----
		{"nested_closures", `import "std/i32";
function adder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function compose(n: i32): (i32) => i32 {
    var f = adder(n);
    function g(x: i32): i32 { return f(f(x)); }
    return g;
}
function main(): i32 { var h = compose(5); print(h(10).to_string()); return 0; }`},

		// ---- enum with string payloads, in an array ----
		{"enum_string_payloads", `import "std/i32";
enum Msg { Text(string), Num(i32), Pair(string, i32) }
function show(m: Msg): string {
    match (m) {
        Text(s) => { return "T:" + s; },
        Num(n) => { return "N:" + n.to_string(); },
        Pair(s, n) => { return "P:" + s + ":" + n.to_string(); }
    }
}
function main(): i32 {
    var msgs: Msg[] = [Text("hi"), Num(42), Pair("k", 7)];
    var i: i32 = 0;
    while (i < msgs.len()) { print(show(msgs[i])); i = i + 1; }
    return 0;
}`},

		// ---- the ? propagation operator ----
		{"try_operator", `import "std/i32";
function parse(s: string): Option[i32] { if (s == "42") { return Some(42); } return None; }
function chain(s: string): Option[i32] { var v = parse(s)?; return Some(v * 2); }
function main(): i32 {
    match (chain("42")) { Some(v) => { print(v.to_string()); }, None => { print("none"); } }
    match (chain("xx")) { Some(v) => { print(v.to_string()); }, None => { print("none"); } }
    return 0;
}`},

		// ---- map keys()/values() iteration ----
		{"map_iteration", `import "std/i32";
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30);
    var total: i32 = 0;
    for k in m.keys() { total = total + k; }
    print(total.to_string());
    var vsum: i32 = 0;
    for v in m.values() { vsum = vsum + v; }
    print(vsum.to_string());
    return 0;
}`},

		// Map iteration order after a non-last delete must agree across
		// the interpreter and every backend. The interp now mirrors the
		// runtime's swap-with-last removal (it previously shifted down,
		// giving a different order). Regression for M3 in
		// docs/ADVERSARIAL-REVIEW-2026-06.md.
		{"map_delete_order", `import "std/i32";
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10); m = m.insert(2, 20); m = m.insert(3, 30); m = m.insert(4, 40);
    m = m.without(1).0;
    var ks = m.keys();
    var i: i32 = 0;
    while (i < ks.len()) { print(ks[i].to_string()); i = i + 1; }
    return 0;
}`},

		// ---- Map copy-on-write (M1) ----
		// Aliasing a map then mutating the alias must NOT bleed into the
		// original — the interp now does rc-based COW like every backend.
		// (Old interp aliased the shared *Map and printed 999/999.)
		{"map_cow_alias_isolation", `import "std/i32";
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 1);
    var n = m;
    n = n.insert(1, 999);
    print(m.get_or(1, -1).to_string());
    print(n.get_or(1, -1).to_string());
    return 0;
}`},
		// A map passed to a function and mutated there (via reassignment
		// of the local param) leaves the caller's map untouched.
		{"map_cow_func_arg", `import "std/i32";
import "core/map";
function bump(p: Map[i32, i32]): i32 {
    p = p.insert(1, 999);
    return p.get_or(1, -1);
}
function main(): i32 {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 1);
    print(bump(m).to_string());
    print(m.get_or(1, -1).to_string());
    return 0;
}`},
		// A map built and returned by a function escapes correctly and is
		// usable by the caller (rc transfers across the return).
		{"map_cow_returned", `import "std/i32";
import "core/map";
function build(): Map[i32, i32] {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    m = m.insert(2, 20);
    return m;
}
function main(): i32 {
    var a: Map[i32, i32] = build();
    var b = a;
    b = b.insert(1, 111);
    print(a.get_or(1, -1).to_string());
    print(b.get_or(1, -1).to_string());
    print(a.get_or(2, -1).to_string());
    return 0;
}`},
		// An alias that dies in an inner scope must not leave the original
		// permanently "shared": a bare mutation afterward still applies.
		{"map_cow_alias_then_scope_exit", `import "std/i32";
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 1);
    { var n = m; n = n.insert(1, 2); }
    m = m.insert(1, 7);
    print(m.get_or(1, -1).to_string());
    return 0;
}`},

		// ---- floats in aggregates (struct / tuple / array / map) ----
		{"float_aggregates", `import "std/float";
import "std/i32";
import "core/map";
struct V3 { x: f64, y: f64, z: f64 }
function dot(a: V3, b: V3): f64 { return a.x*b.x + a.y*b.y + a.z*b.z; }
function main(): i32 {
    var a: V3 = V3{x: 1.0, y: 2.0, z: 3.0};
    var b: V3 = V3{x: 4.0, y: 5.0, z: 6.0};
    print(dot(a, b).to_string());
    var fs: f64[] = [1.5, 2.5, 3.5];
    var sum: f64 = 0.0;
    var i: i32 = 0;
    while (i < fs.len()) { sum = sum + fs[i]; i = i + 1; }
    print(sum.to_string());
    var m: Map[i32, f64] = map_new(8);
    m = m.insert(1, 3.14);
    print(m.get_or(1, 0.0).to_string());
    return 0;
}`},

		// Nested arrays whose FIRST element is a call / ident rather than a
		// literal (#5326): the self-host IR path classified arr-of-arr by
		// literal shape only, so these stride-miscompiled there; native must
		// stay the type-driven oracle on every backend.
		{"nested_arr_call_first_elem", `import "std/i32";
function mkf(): f64[] { return [1.5, 2.5]; }
function mks(): string[] { return ["ab", "cde"]; }
function main(): i32 {
    var mf = [mkf(), [0.25]];
    print((((mf[0][0] + mf[0][1] + mf[1][0]) * 4.0) as i32).to_string());
    var inner = mkf();
    var mi = [inner, [0.5]];
    print((((mi[0][0] + mi[1][0]) * 2.0) as i32).to_string());
    var ms = [mks(), ["f"]];
    print((ms[0][0].len() + ms[0][1].len() + ms[1][0].len()).to_string());
    return 0;
}`},
		// Unannotated array-returning functions (#5326, second cluster): the
		// self-host ret-type inferencer had no ExprArray arm, so these never
		// entered the element-kind registries and a[i] took the 4-byte
		// default stride there. Native must agree everywhere.
		{"arr_ret_fn", `import "std/i32";
function mk2(): f64[] { return [1.5, 2.5]; }
function mk3(): string[] { return ["ab", "cde"]; }
function main(): i32 {
    var a = mk2();
    print((((a[0] + a[1]) * 2.0) as i32).to_string());
    var b = mk3();
    print((b[0].len() + b[1].len()).to_string());
    return 0;
}`},

		// ---- stdlib: json / hex / base64 / math / format ----
		{"stdlib_json", `import "std/json";
import "std/i32";
function main(): i32 {
    match (json.json_parse("{\"a\": 42, \"b\": \"hi\"}")) {
        Some(v) => {
            match (json.json_get_i32(v, "a")) { Some(n) => { print(n.to_string()); }, None => { print("noa"); } }
            match (json.json_get_string(v, "b")) { Some(s) => { print(s); }, None => { print("nob"); } }
        },
        None => { print("parsefail"); }
    }
    return 0;
}`},
		// json_get_f64 routes through std/string's `s.parse_float()`
		// receiver method — the cross-module method reference #5420
		// tracked (std/json's private parser twin is deleted). Scaled
		// i32 prints keep the comparison float-format independent.
		{"stdlib_json_get_f64", `import "std/json";
import "std/i32";
function main(): i32 {
    match (json.json_parse("{\"pi\":3.5,\"big\":2.5e1,\"neg\":-0.5}")) {
        Some(v) => {
            match (json.json_get_f64(v, "pi")) { Some(x) => { print(((x * 2.0) as i32).to_string()); }, None => { print("nopi"); } }
            match (json.json_get_f64(v, "big")) { Some(x) => { print((x as i32).to_string()); }, None => { print("nobig"); } }
            match (json.json_get_f64(v, "neg")) { Some(x) => { print(((x * 0.0 - x * 4.0) as i32).to_string()); }, None => { print("noneg"); } }
        },
        None => { print("parsefail"); }
    }
    return 0;
}`},
		{"stdlib_hex_base64", `import "std/hex";
import "std/base64";
import "std/string";
function main(): i32 {
    print(hex.hex_encode("AB".bytes()));
    print(base64.base64_encode("Hello".bytes()));
    print(string_from_bytes_unchecked(base64.base64_decode("TWFu")));
    print(string_from_bytes_unchecked(hex.hex_decode("4142")));
    return 0;
}`},
		{"stdlib_array_combinators", `import "std/i32";
import "std/array";
function main(): i32 {
    var xs: i32[] = [5, 2, 8, 1, 9, 3];
    var s = xs.sorted_asc();
    print((s[0] as i32).to_string());
    print((s[5] as i32).to_string());
    print(xs.sum().to_string());
    match (xs.median()) { Some(m) => { print(m.to_string()); }, None => {} }
    match (xs.min_max()) { Some(p) => { print(p.0.to_string() + ".." + p.1.to_string()); }, None => {} }
    return 0;
}`},
		// ---- Cell[T] (docs/CELL-TYPE-PLAN.md) ----
		// get / set round-trip: (0+5)*2 = 10, identical on every backend.
		{"cell_get_set", `import "std/i32";
function main(): i32 {
    var c: Cell[i32] = cell_new(0);
    c.set(c.get() + 5);
    c.set(c.get() * 2);
    print(c.get().to_string());
    return 0;
}`},
		// Shared mutation: passing the cell to a function and mutating it
		// there is visible to the caller (the deliberate shared-mutable-state
		// semantics) — 10 bumped three times → 13.
		{"cell_shared_mutation", `import "std/i32";
function bump(c: Cell[i32]): void { c.set(c.get() + 1); }
function main(): i32 {
    var c: Cell[i32] = cell_new(10);
    bump(c); bump(c); bump(c);
    print(c.get().to_string());
    return 0;
}`},
		// A cell as a loop accumulator (the counter idiom) — sum 1..=5 = 15.
		{"cell_accumulator", `import "std/i32";
function main(): i32 {
    var acc: Cell[i32] = cell_new(0);
    var i: i32 = 1;
    while (i <= 5) { acc.set(acc.get() + i); i = i + 1; }
    print(acc.get().to_string());
    return 0;
}`},
		// Cell[string]: construct / get / overwrite-with-concat / read back.
		// Both compilers represent the slot as a single string reference
		// (the Go side rc-tracks it, self-host leaks it) — identical output.
		{"cell_string", `import "std/i32";
import "std/string";
function main(): i32 {
    var c: Cell[string] = cell_new("hi");
    c.set(c.get() + "!");
    print(c.get());
    print(c.get().len().to_string());
    return 0;
}`},
		// Array.build (docs/ARRAY-BUILDER-PLAN.md): the parser desugar runs in
		// both the Go and self-host parsers, so the built array is identical.
		{"array_build", `import "std/i32";
function main(): i32 {
    var out: i32[] = Array.build(function(b: ArrayBuilder[i32]): void {
        var i: i32 = 0;
        while (i < 4) { b.append(i * i); i = i + 1; }
    });
    print(out.len().to_string());
    print(out[3].to_string());
    return 0;
}`},
		// Map.build (docs/ARRAY-BUILDER-PLAN.md): the parser desugar now runs in
		// both the Go and self-host parsers, so the built map is identical.
		{"map_build", `import "std/i32";
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = Map.build(function(b: MapBuilder[i32, i32]): void {
        var i: i32 = 0;
        while (i < 4) { b.insert(i, i * i); i = i + 1; }
    });
    print(m.len().to_string());
    print(m.get_or(3, -1).to_string());
    return 0;
}`},
		// ---- Display spine (#2696): print/write/eprint take any T: Display ----
		// `print(x)` auto-stringifies through Display instead of forcing
		// `print(x.to_string())`. The desugared `.to_string()` call must
		// lower identically on every backend, so the rendered scalars must
		// agree with the interp oracle.
		{"display_print_scalars", `import "std/i32";
import "std/i64";
import "core/cmp";
function main(): i32 {
    var a: i32 = 42;
    var b: i64 = 9000000000;
    var ok: boolean = true;
    print(a);
    print(b);
    print(ok);
    write("x=");
    print(a);
    return 0;
}`},
		// A `@derive(Display)` struct passed straight to `print` renders via
		// the synthesised `to_string`, same bytes on every backend.
		{"display_print_struct", `import "std/i32";
import "core/cmp";
@derive(cmp.Display)
struct Point { x: i32, y: i32 }
function main(): i32 {
    var p: Point = Point { x: 1, y: 2 };
    print(p);
    return 0;
}`},
		// (Bounded-generic `T: Display` forwarded to `print` is covered by
		// the checker test TestCheckPrintDisplayGeneric — this differential
		// oracle doesn't run the monomorphiser, so a generic case is skipped
		// here.)
		// A derived enum routed through print renders its variant form.
		{"display_print_enum", `import "std/i32";
import "core/cmp";
@derive(cmp.Display)
enum Shape { Circle(i32), Empty }
function main(): i32 {
    var c: Shape = Circle(5);
    var e: Shape = Empty;
    print(c);
    print(e);
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
