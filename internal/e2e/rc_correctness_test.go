package e2e

import "testing"

// rcCorpus is the cross-backend rc-correctness regression net for
// the RC/Perceus work (docs/RC-PERCEUS-PLAN.md "Testing strategy"
// → "rc-correctness"). Each program builds up and tears down a
// nested value shape, then returns __rc_underflow_count() (folded
// together with a value-correctness check where useful) — so the
// expected exit code is 0 for every entry. A non-zero exit means
// either the program computed the wrong value (a drop corrupted
// data) or __fern_rc_dec over-released (the detector counter
// fired). This corpus is the go/no-go gate the plan names for
// enabling `free` in step 4: it must stay green on all three
// backends. Drop handlers that only LEAK (closures, maps, generic
// enums, deep nesting) are still exercised here — leaks don't bump
// the underflow counter, so they read 0 too, while any accidental
// over-release shows up immediately.
var rcCorpus = []struct {
	name string
	src  string
}{
	{
		// Array of structs: build, read back, drop at exit.
		name: "array_of_structs",
		src: `struct P { x: i32, y: i32 }
function main(): i32 {
    var ps: P[] = [P{x: 1, y: 2}, P{x: 3, y: 4}];
    return (ps[1].y - 4) + __rc_underflow_count();
}`,
	},
	{
		// Struct holding arrays, aliased then reassigned.
		name: "struct_of_arrays_aliased",
		src: `struct Holder { a: i32[], b: i32[] }
function main(): i32 {
    var h1: Holder = Holder { a: [1, 2], b: [3, 4, 5] };
    var h2: Holder = h1;
    var sum: i32 = h2.a[1] + h2.b[2];
    return (sum - 7) + __rc_underflow_count();
}`,
	},
	{
		// Deeply nested arrays.
		name: "nested_arrays",
		src: `function main(): i32 {
    var cube: i32[][][] = [[[1, 2], [3]], [[4, 5, 6]]];
    return (cube[1][0][2] - 6) + __rc_underflow_count();
}`,
	},
	{
		// Union: build a variant, match it, drop at exit.
		name: "union_build_match",
		src: `struct VInt { v: i32 }
struct VArr { v: i32[] }
type Value = VInt | VArr;
function wrap(xs: i32[]): Value { return VArr { v: xs }; }
function main(): i32 {
    var a: Value = wrap([10, 20, 30]);
    var got: i32 = 0;
    match (a) {
        VInt(n) => { got = n.v; },
        VArr(arr) => { got = arr.v.len(); }
    }
    return (got - 3) + __rc_underflow_count();
}`,
	},
	{
		// Non-uniform enum: pointer payload in one arm, scalar in
		// another (falls through to plain dec — leaks, no underflow).
		name: "enum_non_uniform",
		src: `enum E { Arr(i32[]), Num(i32) }
function main(): i32 {
    var e: E = Arr([1, 2, 3, 4]);
    var n: E = Num(99);
    var got: i32 = 0;
    match (e) { Arr(a) => { got = a.len(); }, Num(_) => { got = 0; } }
    return (got - 4) + __rc_underflow_count();
}`,
	},
	{
		// Closure capturing an array (capture leaks under no-free —
		// must not over-release).
		name: "closure_capture_array",
		src: `function main(): i32 {
    var xs: i32[] = [5, 6, 7];
    var f = function (d: i32): i32 { return xs[2] + d; };
    var got: i32 = f(0);
    return (got - 7) + __rc_underflow_count();
}`,
	},
	{
		// Closure capturing a scalar (no pointer capture).
		name: "closure_capture_scalar",
		src: `function main(): i32 {
    var k: i32 = 42;
    var f = function (x: i32): i32 { return x + k; };
    return (f(0) - 42) + __rc_underflow_count();
}`,
	},
	{
		// Map with string keys/values: build, read, drop.
		name: "map_string_kv",
		src: `function main(): i32 {
    var m: Map[string, string] = map_new(8);
    m = m.set("hello", "world");
    m = m.set("foo", "bar");
    var v: string = m.get_or("hello", "missing");
    return (v.len() - 5) + __rc_underflow_count();
}`,
	},
	{
		// O(N) push loop building an array of structs (push CoW +
		// dec-on-overwrite interplay).
		name: "push_loop_structs",
		src: `struct Node { id: i32 }
function main(): i32 {
    var ns: Node[] = [];
    var i: i32 = 0;
    while (i < 8) {
        ns = ns.push(Node { id: i });
        i = i + 1;
    }
    return (ns.len() - 8) + (ns[7].id - 7) + __rc_underflow_count();
}`,
	},
	{
		// Array-reassignment chain (dec-on-overwrite).
		name: "array_reassign_chain",
		src: `function main(): i32 {
    var xs: i32[] = [1];
    xs = [2, 2];
    xs = [3, 3, 3];
    return (xs.len() - 3) + __rc_underflow_count();
}`,
	},
	{
		// Borrowed parameter: function reads an array through a
		// borrow; caller still owns it afterward.
		name: "borrow_param",
		src: `function sum3(a: i32[]): i32 { return a[0] + a[1] + a[2]; }
function main(): i32 {
    var xs: i32[] = [4, 5, 6];
    var s: i32 = sum3(xs);
    var t: i32 = sum3(xs);
    return (s - 15) + (t - 15) + __rc_underflow_count();
}`,
	},
	{
		// Mixed deep nesting: array of structs that hold arrays +
		// a union, exercised then dropped.
		name: "deep_mixed",
		src: `struct Row { cells: i32[] }
struct Grid { rows: Row[] }
function main(): i32 {
    var g: Grid = Grid { rows: [Row { cells: [1, 2] }, Row { cells: [3, 4, 5] }] };
    return (g.rows[1].cells[2] - 5) + (g.rows.len() - 2) + __rc_underflow_count();
}`,
	},
	{
		// Churn: build + discard an array-of-structs every
		// iteration. Stresses the dec-on-overwrite + drop paths
		// repeatedly; the underflow counter must stay 0 across all
		// iterations (a per-iteration over-release would accumulate).
		name: "churn_loop_structs",
		src: `struct P { x: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 64) {
        var ps: P[] = [P{x: i}, P{x: i + 1}];
        acc = acc + ps[1].x;
        i = i + 1;
    }
    return (acc - 2080) + __rc_underflow_count();
}`,
	},
	{
		// Array of unions, built via push (the checker's array
		// literal doesn't widen mixed variants to the union type),
		// indexed + matched.
		name: "array_of_unions",
		src: `struct VInt { v: i32 }
struct VArr { v: i32[] }
type Value = VInt | VArr;
function vi(n: i32): Value { return VInt { v: n }; }
function va(xs: i32[]): Value { return VArr { v: xs }; }
function main(): i32 {
    var vs: Value[] = [];
    vs = vs.push(vi(1));
    vs = vs.push(va([2, 3]));
    vs = vs.push(vi(4));
    var got: i32 = 0;
    match (vs[1]) { VInt(n) => { got = n.v; }, VArr(a) => { got = a.v[1]; } }
    return (got - 3) + (vs.len() - 3) + __rc_underflow_count();
}`,
	},
	{
		// Struct holding both a union and an array.
		name: "struct_union_and_array",
		src: `struct VInt { v: i32 }
struct VArr { v: i32[] }
type Value = VInt | VArr;
struct Box { tag: Value, data: i32[] }
function main(): i32 {
    var b: Box = Box { tag: VArr { v: [9, 9] }, data: [1, 2, 3] };
    return (b.data.len() - 3) + __rc_underflow_count();
}`,
	},
	{
		// Closure capturing a struct (pointer capture; leaks under
		// no-free, must not over-release).
		name: "closure_capture_struct",
		src: `struct S { v: i32 }
function main(): i32 {
    var s: S = S { v: 21 };
    var f = function (d: i32): i32 { return s.v + d; };
    return (f(0) - 21) + __rc_underflow_count();
}`,
	},
	{
		// Array of closures, each capturing the same array.
		name: "array_of_closures",
		src: `function main(): i32 {
    var base: i32[] = [10, 20, 30];
    var f = function (i: i32): i32 { return base[i]; };
    var g = function (i: i32): i32 { return base[i] + 1; };
    return (f(2) - 30) + (g(0) - 11) + __rc_underflow_count();
}`,
	},
	{
		// Generic enum (Option) wrapping a pointer — falls through
		// to plain dec (leaks, no underflow).
		name: "option_of_array",
		src: `function pick(xs: i32[]): Option[i32[]] {
    if (xs.len() > 0) { return Some(xs); }
    return None;
}
function main(): i32 {
    var got: i32 = 0;
    match (pick([7, 8, 9])) { Some(a) => { got = a[2]; }, None => { got = 0; } }
    return (got - 9) + __rc_underflow_count();
}`,
	},
	{
		// Tuple of arrays, destructured.
		name: "tuple_of_arrays",
		src: `function main(): i32 {
    var t: (i32[], i32[]) = ([1, 2], [3, 4, 5]);
    return (t.0[1] - 2) + (t.1[2] - 5) + __rc_underflow_count();
}`,
	},
	{
		// Map with i32 keys and array values (rc-tracked values).
		name: "map_array_values",
		src: `function main(): i32 {
    var m: Map[i32, i32[]] = map_new(8);
    m = m.set(1, [10, 20]);
    m = m.set(2, [30, 40, 50]);
    var v: i32[] = m.get_or(2, []);
    return (v.len() - 3) + __rc_underflow_count();
}`,
	},
	{
		// Escape into a map value: an owned array built inside a
		// helper escapes via `m.set` (retained without an inc under
		// the borrow model), so it must NOT be freed at the helper's
		// exit. The churn loop reclaims same-size blocks — if `arr`
		// were wrongly freed, a junk array would reuse its block and
		// corrupt the value the map still points at. Mirrors
		// std/url's __query_pair, the case that blocked the flip.
		name: "escape_array_into_map_value",
		src: `function add_pair(m: Map[i32, i32[]], k: i32) {
    var arr: i32[] = [k * 10, k * 10 + 1];
    m.set(k, arr);
}
function main(): i32 {
    var m: Map[i32, i32[]] = map_new(8);
    add_pair(m, 7);
    var c: i32 = 0;
    while (c < 64) {
        var junk: i32[] = [c, c];
        c = c + junk.len();
    }
    var v: i32[] = m.get_or(7, []);
    return (v.len() - 2) + (v[0] - 70) + (v[1] - 71) + __rc_underflow_count();
}`,
	},
	{
		// Escape into a struct field: an owned array stored in a
		// returned struct escapes the helper; freeing it at exit
		// would strand the field. Churn forces reuse of a freed block.
		name: "escape_array_into_struct_field",
		src: `struct Box { items: i32[] }
function mk(n: i32): Box {
    var arr: i32[] = [n, n + 1, n + 2];
    return Box { items: arr };
}
function main(): i32 {
    var b: Box = mk(5);
    var c: i32 = 0;
    while (c < 90) {
        var junk: i32[] = [c, c, c];
        c = c + junk.len();
    }
    return (b.items.len() - 3) + (b.items[2] - 7) + __rc_underflow_count();
}`,
	},
	{
		// Escape into an enum payload (variant constructor arg):
		// the json `JArray(inner)` shape. emitEnumNew stores the
		// payload WITHOUT an inc (unlike StructLit), so an owned
		// array passed in escapes uncounted into the box and must
		// not be freed at the helper's exit. The churn loop both
		// reclaims same-size blocks and accumulates a distinctive
		// value so a reused-block corruption reads back wrong.
		name: "escape_array_into_enum_payload",
		src: `enum Wrap { Arr(i32[]), Empty }
function mk(n: i32): Wrap {
    var arr: i32[] = [n, n + 1, n + 2, n + 3];
    return Arr(arr);
}
function main(): i32 {
    var w: Wrap = mk(1000);
    var c: i32 = 0;
    while (c < 300) {
        var junk: i32[] = [c, c, c, c];
        c = c + 1;
    }
    var got: i32 = 0;
    match (w) {
        Arr(a) => { got = (a.len() - 4) + (a[0] - 1000) + (a[3] - 1003); },
        Empty => { got = 100; }
    }
    return got + __rc_underflow_count();
}`,
	},
	{
		// Escape into a pushed array element: emitArrayPush stores
		// the element without an inc, so an owned inner array
		// pushed into a grid escapes uncounted and must survive the
		// helper's exit. Churn forces reuse of any freed block.
		name: "escape_array_into_pushed_element",
		src: `function add_row(grid: i32[][], n: i32): i32[][] {
    var row: i32[] = [n, n + 1, n + 2, n + 3];
    return grid.push(row);
}
function main(): i32 {
    var grid: i32[][] = [];
    grid = add_row(grid, 5000);
    var c: i32 = 0;
    while (c < 300) {
        var junk: i32[] = [c, c, c, c];
        c = c + 1;
    }
    if (grid.len() != 1) { return 90; }
    var r: i32[] = grid[0];
    return (r.len() - 4) + (r[0] - 5000) + (r[3] - 5003) + __rc_underflow_count();
}`,
	},
	{
		// Escape into an array element via index-assign
		// (`grid[i] = row`): the store retains the value without an
		// inc, so the owned inner array must survive the helper's
		// exit. Churn forces reuse of any freed block.
		name: "escape_array_into_index_assign",
		src: `function fill(grid: i32[][], n: i32) {
    var row: i32[] = [n, n + 1, n + 2, n + 3];
    grid[0] = row;
}
function main(): i32 {
    var grid: i32[][] = [[0, 0, 0, 0]];
    fill(grid, 6000);
    var c: i32 = 0;
    while (c < 300) {
        var junk: i32[] = [c, c, c, c];
        c = c + 1;
    }
    var r: i32[] = grid[0];
    return (r[0] - 6000) + (r[3] - 6003) + __rc_underflow_count();
}`,
	},
	{
		// Escape into a struct field via field-assign
		// (`b.items = arr`): the store retains the value without an
		// inc, so the owned array must survive the helper's exit.
		name: "escape_array_into_field_assign",
		src: `struct Box { items: i32[] }
function fill(b: Box, n: i32) {
    var arr: i32[] = [n, n + 1, n + 2, n + 3];
    b.items = arr;
}
function main(): i32 {
    var b: Box = Box { items: [0, 0, 0, 0] };
    fill(b, 7000);
    var c: i32 = 0;
    while (c < 300) {
        var junk: i32[] = [c, c, c, c];
        c = c + 1;
    }
    return (b.items[0] - 7000) + (b.items[3] - 7003) + __rc_underflow_count();
}`,
	},
	{
		// Map reclamation: each build() owns its map, which is freed
		// (buf + handle returned to the freelist) at the function's
		// exit. 50 build/free cycles churn the same size classes — a
		// corrupted reuse or over-release would drift the checksum.
		// sum_{k=0..49} (16k + 120) = 16*1225 + 6000 = 25600.
		name: "map_owned_churn_free",
		src: `function build(seed: i32): i32 {
    var m: Map[i32, i32] = map_new(8);
    var i: i32 = 0;
    while (i < 16) { m = m.set(i, seed + i); i = i + 1; }
    var sum: i32 = 0;
    var j: i32 = 0;
    while (j < 16) { sum = sum + m.get_or(j, -1); j = j + 1; }
    return sum;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 50) { total = total + build(k); k = k + 1; }
    return (total - 25600) + __rc_underflow_count();
}`,
	},
	{
		// Returned map: make_map builds an owned map and returns it.
		// The return-inc (maps inc as structs in needsRcIncOnAlias)
		// must protect it so make_map's exit drop does NOT free the
		// buf/handle out from under the caller. sum_{k=0..49}(k+2k) =
		// 3*1225 = 3675.
		name: "map_returned_not_freed",
		src: `function make_map(n: i32): Map[i32, i32] {
    var m: Map[i32, i32] = map_new(4);
    m = m.set(1, n);
    m = m.set(2, n * 2);
    return m;
}
function main(): i32 {
    var got: i32 = 0;
    var k: i32 = 0;
    while (k < 50) {
        var m: Map[i32, i32] = make_map(k);
        got = got + m.get_or(1, -1) + m.get_or(2, -1);
        k = k + 1;
    }
    return (got - 3675) + __rc_underflow_count();
}`,
	},
	{
		// Struct-box reclamation: each mk() owns two structs (one with
		// an rc-tracked array field, one all-scalar), freed at the
		// function's exit — fields dropped then the box returned to the
		// freelist. 200 build/free cycles churn the size classes; a
		// corrupted reuse or over-release drifts the checksum.
		// per mk(seed): (a+b) + xs[2] + n = (2*seed+1) + (seed+2) + seed
		//   = 4*seed + 3.  sum_{0..199} (4k+3) = 4*19900 + 600 = 80200.
		name: "struct_box_churn_free",
		src: `struct Pair { a: i32, b: i32 }
struct Holder { xs: i32[], n: i32 }
function mk(seed: i32): i32 {
    var p: Pair = Pair { a: seed, b: seed + 1 };
    var h: Holder = Holder { xs: [seed, seed + 1, seed + 2], n: seed };
    return p.a + p.b + h.xs[2] + h.n;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 80200) + __rc_underflow_count();
}`,
	},
	{
		// Returned struct: make_holder builds an owned struct and
		// returns it. The struct return-inc (needsRcIncOnAlias) must
		// protect it so make_holder's exit drop does NOT free the box
		// out from under the caller. per k: xs[1] + n = (k+1) + k =
		// 2k+1.  sum_{0..49} (2k+1) = 2*1225 + 50 = 2500.
		name: "struct_returned_not_freed",
		src: `struct Holder { xs: i32[], n: i32 }
function make_holder(k: i32): Holder {
    var h: Holder = Holder { xs: [k, k + 1], n: k };
    return h;
}
function main(): i32 {
    var got: i32 = 0;
    var k: i32 = 0;
    while (k < 50) {
        var h: Holder = make_holder(k);
        got = got + h.xs[1] + h.n;
        k = k + 1;
    }
    return (got - 2500) + __rc_underflow_count();
}`,
	},
	{
		// Enum-box reclamation: a uniform enum (both variants carry one
		// i32[], so the box size + droppable layout are static) frees
		// its box at the last reference. 200 build/free cycles churn
		// the box size class; a corrupted reuse or over-release drifts
		// the checksum. mk(seed)=seed+2; sum_{0..199}(k+2)=19900+400.
		name: "enum_box_churn_free",
		src: `enum Wrap { A(i32[]), B(i32[]) }
function mk(seed: i32): i32 {
    var w: Wrap = A([seed, seed + 1, seed + 2]);
    var got: i32 = 0;
    match (w) {
        A(xs) => { got = xs[2]; },
        B(xs) => { got = xs[0]; }
    }
    return got;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 20300) + __rc_underflow_count();
}`,
	},
	{
		// Returned enum: make_w builds an owned enum and returns it.
		// The enum return-inc (needsRcIncOnAlias) must protect it so
		// make_w's exit drop does NOT free the box out from under the
		// caller. xs[1] = k+1; sum_{0..49}(k+1) = 1225 + 50 = 1275.
		name: "enum_returned_not_freed",
		src: `enum Wrap { A(i32[]), B(i32[]) }
function make_w(k: i32): Wrap {
    var w: Wrap = A([k, k + 1, k + 2]);
    return w;
}
function main(): i32 {
    var got: i32 = 0;
    var k: i32 = 0;
    while (k < 50) {
        var w: Wrap = make_w(k);
        match (w) {
            A(xs) => { got = got + xs[1]; },
            B(xs) => { got = got + xs[0]; }
        }
        k = k + 1;
    }
    return (got - 1275) + __rc_underflow_count();
}`,
	},
	{
		// Non-uniform enum: I(i32) and A(i32[]) differ in droppable
		// layout AND box size, so reclamation needs the per-tag size
		// dispatch (the JsonValue shape). Churns both distinctly-sized
		// boxes; a wrong-size free or over-release drifts the checksum.
		// v=A => xs[1]=seed+1; w=I => +seed; got=2*seed+1;
		// sum_{0..199}(2k+1) = 2*19900 + 200 = 40000.
		name: "enum_nonuniform_box_free",
		src: `enum V { I(i32), A(i32[]) }
function mk(seed: i32): i32 {
    var v: V = A([seed, seed + 1]);
    var w: V = I(seed);
    var got: i32 = 0;
    match (v) { I(n) => { got = n; }, A(xs) => { got = xs[1]; } }
    match (w) { I(n) => { got = got + n; }, A(xs) => { got = got + xs[0]; } }
    return got;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 40000) + __rc_underflow_count();
}`,
	},
	{
		// Real stdlib hot path: query_parse builds a
		// Map[string, string[]] (map-structural reclamation + the
		// escape-out path + string[] values). Round-trips a few keys
		// and checks the underflow counter stays 0 under free.
		name: "stdlib_query_parse_roundtrip",
		src: `function main(): i32 {
    var bad: i32 = 0;
    var m: Map[string, string[]] = query_parse("a=1&b=2&tag=x&tag=y");
    if (m.len() != 3) { bad = bad + 1; }
    match (m.get("tag")) {
        Some(arr) => { if (arr.len() != 2) { bad = bad + 10; } },
        None => { bad = bad + 100; }
    }
    match (m.get("a")) {
        Some(arr) => { if (arr[0] != "1") { bad = bad + 1000; } },
        None => { bad = bad + 10000; }
    }
    return bad + __rc_underflow_count();
}`,
	},
	{
		// Real stdlib hot path: json_parse builds a JsonValue tree
		// (non-uniform enum box reclamation + nested Map / array), and
		// json_encode walks it back. Exercises the per-tag enum drop +
		// map drop together; the underflow counter must stay 0.
		name: "stdlib_json_roundtrip",
		src: `function main(): i32 {
    var bad: i32 = 0;
    match (json_parse("{\"a\":[1,2,3],\"b\":\"hi\"}")) {
        Some(v) => {
            match (v) {
                JObject(m) => { if (m.len() != 2) { bad = bad + 1; } },
                _ => { bad = bad + 10; }
            }
        },
        None => { bad = bad + 100; }
    }
    var arr: JsonValue[] = [JNumber("1"), JBool(true)];
    if (json_encode(JArray(arr)) != "[1,true]") { bad = bad + 1000; }
    return bad + __rc_underflow_count();
}`,
	},
	{
		// Map growth reclamation: inserting 100 keys into one map
		// triggers several __map_grow doublings (cap 4→8→…→128), each
		// freeing the previous kv buffer. The read-back checksum + the
		// underflow counter catch a corrupted copy or a freed buffer
		// that was still referenced. sum_{0..99} 2i = 2*4950 = 9900.
		name: "map_growth_buffer_free",
		src: `function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    var i: i32 = 0;
    while (i < 100) { m = m.set(i, i * 2); i = i + 1; }
    var sum: i32 = 0;
    var j: i32 = 0;
    while (j < 100) { sum = sum + m.get_or(j, -1); j = j + 1; }
    return (sum - 9900) + __rc_underflow_count();
}`,
	},
	{
		// Map-value reclamation: i32[] values (valKind 2 = plain-elem
		// array → arr_dec). Fresh literals transfer rc=1 to the map;
		// get_or results bind to locals (inc-on-get balanced by the
		// local's exit-sweep dec); map_drop_values frees each value.
		name: "map_i32_array_values",
		src: `function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    m = m.set(1, [10, 20, 30]);
    m = m.set(2, [40, 50]);
    var v1: i32[] = m.get_or(1, []);
    var v2: i32[] = m.get_or(2, []);
    return (v1[2] + v2[0] - 70) + __rc_underflow_count();
}`,
	},
	{
		// Map-value reclamation: string[] values. string elements are
		// not rc-tracked, so string[] is a plain-elem array (valKind 2
		// → arr_dec frees the buffer; the strings themselves leak, as
		// in standalone array reclamation).
		name: "map_string_array_values",
		src: `function main(): i32 {
    var m: Map[string, string[]] = map_new(4);
    m = m.set("a", ["x", "yy", "zzz"]);
    var v: string[] = m.get_or("a", []);
    return (v[2].len() - 3) + __rc_underflow_count();
}`,
	},
	{
		// Map-value reclamation: i32[][] values (valKind 3 = rc-elem
		// array → drop_arr_ptr recurses one level, dec'ing each inner
		// i32[], then frees the outer buffer). Exercises the rc-elem
		// value free path the other map cases don't.
		name: "map_nested_array_values",
		src: `function main(): i32 {
    var m: Map[i32, i32[][]] = map_new(4);
    m = m.set(1, [[1, 2], [3, 4, 5]]);
    var v: i32[][] = m.get_or(1, []);
    return (v[1].len() + v[0][1] - 5) + __rc_underflow_count();
}`,
	},
	{
		// Aliased array value: the set value is an Ident
		// (needsRcIncOnAlias → inc-on-set), so the source local's
		// exit dec and the map's drop balance to a single free.
		name: "map_aliased_array_value",
		src: `function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    var arr: i32[] = [7, 8, 9];
    m = m.set(5, arr);
    var v: i32[] = m.get_or(5, []);
    return (v[1] - 8) + __rc_underflow_count();
}`,
	},
	{
		// Escaping get-result: a borrowed-map helper returns one of
		// its values past the map's frame. inc-on-get keeps the value
		// alive (rc survives the owner map's drop in main); the caller
		// local owns the surviving reference.
		name: "map_value_escapes_return",
		src: `function lookup(m: Map[i32, i32[]], k: i32): i32[] {
    return m.get_or(k, []);
}
function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    m = m.set(1, [100, 200]);
    var got: i32[] = lookup(m, 1);
    return (got[1] - 200) + __rc_underflow_count();
}`,
	},
	{
		// get → push → overwrite-set (the std/url query_parse shape).
		// The overwrite frees the prior value (overwrite-dec), but here
		// a live get-borrow (cur) holds it, so the rc-aware dec only
		// decrements; the final plain borrow-dec leaks it. Either way
		// the counter must stay 0 (no over-release).
		name: "map_get_push_overwrite",
		src: `function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    m = m.set(1, [10]);
    match (m.get(1)) {
        Some(cur) => { m = m.set(1, cur.push(20)); },
        None => {}
    }
    var v: i32[] = m.get_or(1, []);
    return (v.len() - 2) + (v[1] - 20) + __rc_underflow_count();
}`,
	},
	{
		// Overwrite-dec under churn: 200 fresh arrays stored at the
		// same key. With no live borrow, each overwrite frees the prior
		// value at rc==1 → the freelist recycles, memory stays bounded,
		// and the counter stays 0. Last write is [199,200,201].
		name: "map_overwrite_churn_free",
		src: `function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    var i: i32 = 0;
    while (i < 200) { m = m.set(7, [i, i + 1, i + 2]); i = i + 1; }
    var v: i32[] = m.get_or(7, []);
    return (v[2] - 201) + __rc_underflow_count();
}`,
	},
	{
		// Overwrite-dec is rc-aware: an outstanding get-borrow of the
		// old value (inc-on-get → rc 2) must NOT be freed by the
		// overwrite (rc 2→1, no free). The borrow stays readable; the
		// counter stays 0.
		name: "map_overwrite_with_live_borrow",
		src: `function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    m = m.set(1, [10, 11]);
    var borrow: i32[] = m.get_or(1, []);
    m = m.set(1, [20, 21]);
    var x: i32 = borrow[1];
    var y: i32 = m.get_or(1, [])[0];
    return (x + y - 31) + __rc_underflow_count();
}`,
	},
	{
		// m.values() snapshot of an array-valued map: each element is
		// retained (the snapshot co-owns), so dropping the snapshot
		// and the map balances to a single free per value.
		name: "map_values_array_snapshot",
		src: `function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    m = m.set(1, [3, 4]);
    m = m.set(2, [5, 6, 7]);
    var vs: i32[][] = m.values();
    var sum: i32 = 0;
    var i: i32 = 0;
    while (i < vs.len()) { sum = sum + vs[i].len(); i = i + 1; }
    return (sum - 5) + __rc_underflow_count();
}`,
	},
}

func TestX86_64RcCorrectnessCorpus(t *testing.T) {
	for _, c := range rcCorpus {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, c.src); code != 0 {
				t.Errorf("%s: got exit %d, want 0 (wrong value or rc over-release)", c.name, code)
			}
		})
	}
}

func TestArm64RcCorrectnessCorpus(t *testing.T) {
	for _, c := range rcCorpus {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != 0 {
				t.Errorf("%s: got exit %d, want 0 (wrong value or rc over-release)", c.name, code)
			}
		})
	}
}

func TestWASMRcCorrectnessCorpus(t *testing.T) {
	for _, c := range rcCorpus {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("%s: got exit %d, want 0 (wrong value or rc over-release)", c.name, got)
			}
		})
	}
}
