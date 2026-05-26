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
		// Closure env churn: create + call + drop a fresh closure each
		// iteration. With reclamation the env rc1 block frees at the
		// loop-body scope exit and the next alloc reuses it; the
		// counter must stay 0 (no over-release). sum_{1..100} = 5050.
		name: "closure_churn_free",
		src: `function main(): i32 {
    var sum: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var base: i32 = i;
        var f = function (x: i32): i32 { return base + x; };
        sum = sum + f(1);
        i = i + 1;
    }
    return (sum - 5050) + __rc_underflow_count();
}`,
	},
	{
		// Escaping closure: a factory returns its closure past its own
		// frame. The factory must NOT free the env (move-on-return);
		// main owns the surviving closure and frees it at exit. f(5)
		// with n=10 → 15.
		name: "closure_escapes_return",
		src: `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    var f = makeAdder(10);
    return (f(5) - 15) + __rc_underflow_count();
}`,
	},
	{
		// Stage 3: closure capturing an ARRAY, churned 100x. The
		// per-closure drop thunk frees the captured array (arr_dec) at
		// each closure's death so the freelist recycles; the counter
		// must stay 0 (the array was inc'd once on capture, dropped
		// once on closure death). acc = sum_{i=0..99}(i+2) = 5150.
		name: "closure_array_capture_churn",
		src: `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var xs: i32[] = [i, i + 1, i + 2];
        var f = function (d: i32): i32 { return xs[2] + d; };
        acc = acc + f(0);
        i = i + 1;
    }
    return (acc - 5150) + __rc_underflow_count();
}`,
	},
	{
		// Stage 3 safety: a NESTED closure captures the outer array via
		// a CaptureRef (not inc'd at MakeEnv), so its drop must fall
		// back to the generic env-only path — the per-closure thunk's
		// unconditional capture-drop would over-release it. outer(0) →
		// inner(1) → xs[2] + 0 + 1 = 31. Counter must stay 0.
		name: "closure_nested_capture",
		src: `function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var outer = function (a: i32): i32 {
        var inner = function (b: i32): i32 { return xs[2] + a + b; };
        return inner(1);
    };
    return (outer(0) - 31) + __rc_underflow_count();
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
		// Tuple BOX reclamation: a scalar tuple (i32, i32) leaked its
		// whole heap box entirely (tuples carried no rc header and were
		// never swept). It now carries an rc=1 header and returns the
		// box to the freelist at the owning local's last reference. 200
		// build/free cycles churn the box size class; a corrupted reuse
		// or over-release drifts the checksum / underflow count.
		// mk(seed) = 2*seed + 1; sum_{0..199}(2k+1) = 2*19900 + 200.
		name: "tuple_scalar_box_churn_free",
		src: `function mk(seed: i32): i32 {
    var t: (i32, i32) = (seed, seed + 1);
    return t.0 + t.1;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 40000) + __rc_underflow_count();
}`,
	},
	{
		// Destructure temp box reclamation: `var (a, b) = (i, i+1)`
		// builds a tuple box, extracts the elements, and the box is pure
		// overhead afterward. The temp is an owned tuple local, so its
		// box frees at scope exit — extracting a/b (scalar here) doesn't
		// alias the box. a + b = 2i + 1; sum_{0..199} = 40000.
		name: "tuple_destructure_box_churn_free",
		src: `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var (a, b) = (i, i + 1);
        acc = acc + a + b;
        i = i + 1;
    }
    return (acc - 40000) + __rc_underflow_count();
}`,
	},
	{
		// Returned tuple: mk builds an owned tuple box and returns it.
		// Move-on-return (isOwnedRcLocal now covers tuples) must skip the
		// exit dec so mk's box_free does NOT free the box out from under
		// the caller. Churn forces same-size reuse that would corrupt a
		// strayed read. t.0 + t.1 = 7 + 8 = 15.
		name: "tuple_returned_escapes",
		src: `function mk(n: i32): (i32, i32) { return (n, n + 1); }
function main(): i32 {
    var t: (i32, i32) = mk(7);
    var c: i32 = 0;
    while (c < 200) { var junk: i32[] = [c, c]; c = c + 1; }
    return (t.0 + t.1 - 15) + __rc_underflow_count();
}`,
	},
	{
		// Tuple-to-tuple alias, both live: `var t2 = t1` inc's the box
		// (needsRcIncOnAlias), so the two exit decs free it exactly once
		// (the first sees rc==2 and just dec's; the second sees rc==1 and
		// box_free's). Without the alias inc this double-frees.
		// t1.0 + t2.1 = 2*seed + 1; sum_{0..199} = 40000.
		name: "tuple_alias_box_churn_free",
		src: `function mk(seed: i32): i32 {
    var t1: (i32, i32) = (seed, seed + 1);
    var t2: (i32, i32) = t1;
    return t1.0 + t2.1;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 40000) + __rc_underflow_count();
}`,
	},
	{
		// Destructure-of-arrays that ESCAPES: `var (a, b) = (...)` extracts
		// element ARRAY pointers from the tuple box; dup-on-projection
		// gives `a` its own counted reference. `return a` takes
		// move-on-return (no inc, sweep-excluded), and the tuple's
		// deep-drop only dec's arrA (rc 2→1, not free) — so the returned
		// buffer stays valid for the caller. Without the dup, the tuple
		// deep-drop would free arrA out from under the return (UAF).
		// a = [10, 20]; r[1] = 20; sum_{0..99} 20 = 2000.
		name: "tuple_destructure_array_escapes",
		src: `function mk(): i32[] {
    var (a, b) = ([10, 20], [30, 40, 50]);
    return a;
}
function main(): i32 {
    var got: i32 = 0;
    var k: i32 = 0;
    while (k < 100) {
        var r: i32[] = mk();
        got = got + r[1];
        k = k + 1;
    }
    return (got - 2000) + __rc_underflow_count();
}`,
	},
	{
		// Destructure of an ALIASED tuple local: `var (a, b) = t` copies
		// t's box pointer into the destructure temp. Both t and the temp
		// are owned tuple locals that box_free at exit, so the temp store
		// must inc the box — else the two frees double-free it (a
		// nondeterministic heap corruption / OOB). The underflow detector
		// + churn catch the missing inc. a + b = 2*seed + 1; sum = 40000.
		name: "tuple_alias_destructure_churn_free",
		src: `function mk(seed: i32): i32 {
    var t: (i32, i32) = (seed, seed + 1);
    var (a, b) = t;
    return a + b;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 40000) + __rc_underflow_count();
}`,
	},
	{
		// Nested-tuple destructure: `var (a, b) = t; var (c, d) = b` where
		// b is an inner tuple extracted from t. Each destructure temp
		// aliases a distinct box (t's outer, b's inner), so each needs its
		// own alias inc; a missing inc double-frees the outer or inner box
		// (the TestWASMTupleNestedTuple OOB regression). Churned to surface
		// freelist corruption. a + c + d = 3*seed + 3; sum_{0..199} = 60300.
		name: "tuple_nested_destructure_churn_free",
		src: `function mk(seed: i32): i32 {
    var t: (i32, (i32, i32)) = (seed, (seed + 1, seed + 2));
    var (a, b) = t;
    var (c, d) = b;
    return a + c + d;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 60300) + __rc_underflow_count();
}`,
	},
	{
		// Destructured ARRAY bindings reclaim their buffers. `var (a, b) =
		// ([..], [..])` extracts two array pointers; dup-on-projection
		// makes a/b owned array locals that arr_dec-free their buffers at
		// scope exit, while the tuple's deep-drop dec's its own element
		// refs + frees the box. Previously the bindings were tainted and
		// leaked every buffer. Churned 200x — a leak would grow the heap
		// and a miscount would drift the underflow detector. a[1] = k+1,
		// b[2] = k+4; sum_{0..199}(2k+5) = 2*19900 + 1000 = 40800.
		name: "tuple_destructure_arrays_reclaim_churn",
		src: `function mk(k: i32): i32 {
    var (a, b) = ([k, k + 1], [k + 2, k + 3, k + 4]);
    return a[1] + b[2];
}
function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { total = total + mk(i); i = i + 1; }
    return (total - 40800) + __rc_underflow_count();
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
		// Map with STRUCT values (valKind 4). Previously struct map values
		// leaked entirely — not retained on set/get, not dropped. They now
		// route through the generated __drop_map_struct_<Item> loop
		// (deep-dropping each value via __drop_struct_Item → its box + xs
		// buffer) at the map's last reference, with set/get retains
		// balancing it. Churned 200x: a leak grows the heap, a miscount
		// drifts the underflow detector. it.xs[1] = seed+1;
		// sum_{0..199}(k+1) = 19900 + 200 = 20100.
		name: "map_struct_values_churn_free",
		src: `struct Item { xs: i32[] }
function mk(seed: i32): i32 {
    var m: Map[i32, Item] = map_new(8);
    m = m.set(seed, Item { xs: [seed, seed + 1] });
    m = m.set(seed + 1, Item { xs: [seed + 2] });
    var it: Item = m.get_or(seed, Item { xs: [0] });
    return it.xs[1];
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 20100) + __rc_underflow_count();
}`,
	},
	{
		// Struct map value that ESCAPES: a value read out via get_or is
		// retained (inc) so it survives the map's deep-drop at the
		// helper's exit. The returned Item must stay valid + uncorrupted
		// across 200 churn iterations. it.xs[1] = c+1; sum = 20100.
		name: "map_struct_value_escapes",
		src: `struct Item { xs: i32[] }
function mk(n: i32): Item {
    var m: Map[i32, Item] = map_new(4);
    m = m.set(0, Item { xs: [n, n + 1] });
    return m.get_or(0, Item { xs: [0] });
}
function main(): i32 {
    var got: i32 = 0;
    var c: i32 = 0;
    while (c < 200) {
        var it: Item = mk(c);
        got = got + it.xs[1];
        c = c + 1;
    }
    return (got - 20100) + __rc_underflow_count();
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
	{
		// Like map_values_array_snapshot, but dereferences the snapshot's
		// inner elements (vs[i][j]) rather than only their lengths. The
		// snapshot must store each value as a bare array POINTER at
		// ptr-width stride — not a string-shaped two-word cell — or
		// indexing reads a mangled element on wasm32 (the array header's
		// length word interpreted as a pointer) and traps.
		name: "map_values_array_elem_read",
		src: `function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    m = m.set(1, [3, 4]);
    m = m.set(2, [5, 6, 7]);
    var vs: i32[][] = m.values();
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < vs.len()) {
        var inner: i32[] = vs[i];
        var j: i32 = 0;
        while (j < inner.len()) { acc = acc + inner[j]; j = j + 1; }
        i = i + 1;
    }
    // 3+4+5+6+7 = 25.
    return (acc - 25) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation Stage A — nested struct churn: an
		// Outer holding an Inner (which itself holds an array) is
		// built + discarded every iteration. Stage A recurses the
		// Outer drop into __drop_struct_Inner (freeing Inner's box
		// AND its array buffer), where the pre-transitive drop only
		// flat-dec'd Inner (leak). A per-iteration over-release of
		// the now-freed Inner box would accumulate on the underflow
		// counter. sum_{i=0..99}(i+2) = 4950 + 200 = 5150.
		name: "nested_struct_churn_free",
		src: `struct Inner { vals: i32[] }
struct Outer { inner: Inner, tag: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var o: Outer = Outer { inner: Inner { vals: [i, i + 1, i + 2] }, tag: i };
        acc = acc + o.inner.vals[2];
        i = i + 1;
    }
    return (acc - 5150) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation Stage A — uniform-enum-of-struct
		// churn: a union whose variants each carry a struct holding
		// an array, built + matched + discarded every iteration. The
		// enum drop now recurses into __drop_struct_VInt (freeing the
		// struct box + its array) rather than flat-dec'ing the
		// payload. sum_{i=0..99}(i+1) = 4950 + 100 = 5050.
		name: "enum_of_struct_churn_free",
		src: `struct VInt { v: i32[] }
struct VArr { v: i32[] }
type Value = VInt | VArr;
function mk(n: i32): Value { return VInt { v: [n, n + 1] }; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var x: Value = mk(i);
        match (x) { VInt(a) => { acc = acc + a.v[1]; }, VArr(b) => { acc = acc + b.v[0]; } }
        i = i + 1;
    }
    return (acc - 5050) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation Stage A — escape guard: a nested
		// struct (Inner) inside a RETURNED Outer must NOT be freed at
		// the constructor's exit. The deep recursive drop is
		// is_unique-gated, and the return-inc keeps Outer (and hence
		// Inner) live, so the churn loop's same-size reuse must not
		// corrupt the surviving Inner.vals.
		name: "nested_struct_escapes_return",
		src: `struct Inner { vals: i32[] }
struct Outer { inner: Inner }
function mk(n: i32): Outer { return Outer { inner: Inner { vals: [n, n + 1] } }; }
function main(): i32 {
    var o: Outer = mk(42);
    var c: i32 = 0;
    while (c < 200) {
        var junk: i32[] = [c, c];
        c = c + 1;
    }
    return (o.inner.vals[0] - 42) + (o.inner.vals[1] - 43) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation Stage A — shared nested struct: two
		// Outers share the same Inner (aliased), so when the first
		// Outer drops, Inner is rc>1 and must only dec (not free).
		// The is_unique gate inside __drop_struct_Inner is what makes
		// this safe; a premature free would corrupt the second read.
		name: "shared_nested_struct_no_free",
		src: `struct Inner { vals: i32[] }
struct Outer { inner: Inner }
function main(): i32 {
    var shared: Inner = Inner { vals: [7, 8, 9] };
    var a: Outer = Outer { inner: shared };
    var b: Outer = Outer { inner: shared };
    var first: i32 = a.inner.vals[2];
    var second: i32 = b.inner.vals[2];
    return (first - 9) + (second - 9) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation Stage A — escaped struct with a nested
		// struct field, churned. `o` escapes into the array (owned-but-
		// not-free-eligible), so its drop takes the non-eligible branch,
		// which keeps the flat one-level field dec — deep recursion (and
		// the free it implies) fires only in the eligible branch. The
		// shared inner is also held by a live local, so its rc stays > 1
		// and the is_unique gate is exercised too. (The non-eligible
		// deep-free regression itself is caught end-to-end by the
		// self-host suite, whose parser threads structs through result
		// shapes in a way no small corpus program reproduces.)
		name: "escaped_struct_nested_field_no_free",
		src: `struct Inner { vals: i32[] }
struct Outer { inner: Inner }
function main(): i32 {
    var shared: Inner = Inner { vals: [1, 2, 3] };
    var keep: Outer[] = [];
    var o: Outer = Outer { inner: shared };
    keep = keep.push(o);
    var c: i32 = 0;
    while (c < 200) {
        var junk: i32[] = [c, c, c];
        c = c + 1;
    }
    return (keep[0].inner.vals[1] - 2) + (keep.len() - 1) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation Stage B — array of structs (each
		// holding an array), built + discarded every iteration. The
		// eligible array drop now recurses through __drop_arr_struct_Item
		// → __drop_struct_Item per element, freeing each element box AND
		// its array buffer, where the pre-Stage-B __fern_drop_arr_ptr
		// only flat-dec'd the element pointers (leaking the boxes). A
		// per-iteration over-release of a now-freed element box would
		// accumulate on the underflow counter. sum_{i=0..99}((i+1)+(i+2))
		// = 2*4950 + 300 = 10200.
		name: "arr_of_struct_deep_churn_free",
		src: `struct Item { tags: i32[] }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var items: Item[] = [Item { tags: [i, i + 1] }, Item { tags: [i + 2] }];
        acc = acc + items[0].tags[1] + items[1].tags[0];
        i = i + 1;
    }
    return (acc - 10200) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation Stage B — array of CHILDLESS structs:
		// the element struct has no rc fields, so __drop_struct_P just
		// frees the element box on its last reference (the loop now
		// reclaims element boxes that drop_arr_ptr's flat dec leaked).
		// sum_{i=0..99}(i+1) = 5050.
		name: "arr_of_childless_struct_churn_free",
		src: `struct P { x: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var ps: P[] = [P { x: i }, P { x: i + 1 }];
        acc = acc + ps[1].x;
        i = i + 1;
    }
    return (acc - 5050) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation Stage B — array of structs that
		// ESCAPES (returned from mk): the array must not be freed at the
		// constructor's exit, so its element boxes survive. The churn
		// loop forces same-size reuse that would corrupt a strayed read.
		name: "arr_of_struct_escapes_return",
		src: `struct Item { tags: i32[] }
function mk(n: i32): Item[] {
    var xs: Item[] = [Item { tags: [n, n + 1] }, Item { tags: [n + 2] }];
    return xs;
}
function main(): i32 {
    var keep: Item[] = mk(7);
    var c: i32 = 0;
    while (c < 200) {
        var junk: i32[] = [c, c];
        c = c + 1;
    }
    return (keep[0].tags[1] - 8) + (keep[1].tags[0] - 9) + (keep.len() - 2) + __rc_underflow_count();
}`,
	},
	{
		// Struct-literal locals are now free-eligible (rhsTainted no
		// longer taints a StructLit RHS — a fresh struct is owned, not
		// an alias). A struct local built from a literal and churned now
		// frees its box each iteration; an over-release would drift the
		// counter. sum_{i=0..99}(i+1) = 5050.
		name: "struct_literal_local_churn_free",
		src: `struct Pt { xs: i32[], y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var p: Pt = Pt { xs: [i, i + 1], y: i };
        acc = acc + p.xs[1];
        i = i + 1;
    }
    return (acc - 5050) + __rc_underflow_count();
}`,
	},
	{
		// Conditional alias via an if-expression: `v1` aliases the
		// struct `v0` through an arm, so v0 must NOT be freed at scope
		// exit (the alias-inc doesn't fire through a conditional). The
		// escape walk taints v0; the churn loop forces same-size reuse
		// that would corrupt a strayed read. Reads both to keep the
		// alias live.
		name: "ifexpr_alias_struct_no_free",
		src: `struct Pt { xs: i32[], y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var v0: Pt = Pt { xs: [i, i + 1], y: i };
        var v1: Pt = if (i < 50) { v0 } else { v0 };
        acc = acc + v1.xs[1] + v0.y;
        i = i + 1;
    }
    return (acc - 10000) + __rc_underflow_count();
}`,
	},
	{
		// Conditional alias via a match-expression — the shape that was
		// a latent use-after-free even for arrays before the escape-walk
		// fix. `a1` aliases `a0` through the match arms; a0 must survive
		// until both are read. Churned to force block reuse.
		name: "matchexpr_alias_array_no_free",
		src: `function pick_arr(o: Option[i32], a0: i32[]): i32[] {
    return match (o) { Some(x) => a0, None => a0 };
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var a0: i32[] = [i, i + 1, i + 2];
        var a1: i32[] = match (Some(i)) { Some(x) => a0, None => a0 };
        acc = acc + a1[2] + a0[0];
        i = i + 1;
    }
    return (acc - 10100) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation Stage C — non-uniform enum with a
		// struct payload, churned. The eligible enum drop's tag-dispatch
		// (variant-plan) arm knows the exact payload type, so the Leaf
		// arm recurses through __drop_struct_Item (freeing the Item box
		// + its array) instead of flat-dec'ing the payload. Reachable
		// now that composite-literal RHS (Item{...} fed to the Leaf
		// constructor) is free-eligible. sum_{i=0..99}(i+1) = 5050.
		name: "nonuniform_enum_struct_payload_churn_free",
		src: `struct Item { tags: i32[] }
enum Node { Leaf(Item), Branch(i32) }
function mk(n: i32): Node { return Leaf(Item { tags: [n, n + 1] }); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var nd: Node = mk(i);
        match (nd) { Leaf(it) => { acc = acc + it.tags[1]; }, Branch(b) => { acc = acc + b; } }
        i = i + 1;
    }
    return (acc - 5050) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation Stage C — enum carrying a struct
		// payload that ESCAPES (pushed into an array): the enum is
		// tainted (not free-eligible), so its struct payload must NOT be
		// deep-freed; the array still references it after the local is
		// swept. Churn forces same-size reuse that would corrupt a
		// strayed read.
		name: "enum_struct_payload_escapes",
		src: `struct Item { tags: i32[] }
enum Node { Leaf(Item), Branch(i32) }
function main(): i32 {
    var keep: Node[] = [];
    keep = keep.push(Leaf(Item { tags: [5, 6] }));
    var c: i32 = 0;
    while (c < 200) {
        var junk: i32[] = [c, c];
        c = c + 1;
    }
    var got: i32 = 0;
    match (keep[0]) { Leaf(it) => { got = it.tags[1]; }, Branch(b) => { got = b; } }
    return (got - 6) + (keep.len() - 1) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation — CHILDLESS nested-struct field box.
		// Outer holds a Pt with no rc-tracked fields; dropFnNameFor now
		// routes it through __drop_struct_Pt (is_unique → box_free)
		// instead of the flat dec that leaked the Pt box. Churned so a
		// per-iteration over-release would accumulate. sum_{i=0..99}(i+1)
		// = 5050.
		name: "childless_nested_struct_field_free",
		src: `struct Pt { x: i32, y: i32 }
struct Outer { inner: Pt, tag: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var o: Outer = Outer { inner: Pt { x: i, y: i + 1 }, tag: i };
        acc = acc + o.inner.y;
        i = i + 1;
    }
    return (acc - 5050) + __rc_underflow_count();
}`,
	},
	{
		// Childless struct field that ESCAPES (Outer returned): the Pt
		// box must NOT be freed at the constructor's exit; the returned
		// Outer still references it. Churn forces same-size reuse.
		name: "childless_nested_struct_escapes",
		src: `struct Pt { x: i32, y: i32 }
struct Outer { inner: Pt, tag: i32 }
function mk(n: i32): Outer { return Outer { inner: Pt { x: n, y: n + 1 }, tag: n }; }
function main(): i32 {
    var o: Outer = mk(42);
    var c: i32 = 0;
    while (c < 200) {
        var junk: i32[] = [c, c];
        c = c + 1;
    }
    return (o.inner.y - 43) + (o.tag - 42) + __rc_underflow_count();
}`,
	},
	{
		// Closure-capture composite — a closure capturing a STRUCT now
		// deep-drops it (the per-closure drop thunk routes the struct
		// capture through __drop_struct_Item, freeing its box) instead
		// of the flat one-level rc_dec. The thunk only runs when every
		// capture was inc'd at MakeEnv, so this is balanced. Churned:
		// an over-release would accumulate. sum_{i=0..99}(i+1) = 5050.
		name: "closure_captures_struct_churn_free",
		src: `struct Item { tags: i32[] }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var it: Item = Item { tags: [i, i + 1] };
        var f = function (d: i32): i32 { return it.tags[1] + d; };
        acc = acc + f(0);
        i = i + 1;
    }
    return (acc - 5050) + __rc_underflow_count();
}`,
	},
	{
		// Closure-capture composite — a closure capturing an ARRAY OF
		// STRUCTS deep-drops each element box via __drop_arr_struct_Item
		// (Stage B loop) at the closure's death, not just the buffer.
		// Two elements per iter → f() returns len 2.
		name: "closure_captures_arr_of_struct_churn_free",
		src: `struct Item { tags: i32[] }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var items: Item[] = [Item { tags: [i, i + 1] }, Item { tags: [i + 2] }];
        var f = function (d: i32): i32 { return items.len() + d; };
        acc = acc + f(0);
        i = i + 1;
    }
    return (acc - 200) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation — UNIFORM union with struct payloads.
		// VInt | VArr both carry a struct ptr at the same offset, so the
		// drop used to take the branchless uniform path and flat-dec the
		// variant struct box (leak). It now steers to the tag-dispatch
		// variant-plan path (enumHasStructPayload), whose Leaf-style arm
		// deep-drops the exact payload type via __drop_struct_<T>.
		// sum_{i=0..99}(i+1) = 5050.
		name: "uniform_union_struct_payload_churn_free",
		src: `struct VInt { v: i32[] }
struct VArr { v: i32[] }
type Value = VInt | VArr;
function mk(n: i32): Value { return VInt { v: [n, n + 1] }; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var x: Value = mk(i);
        match (x) { VInt(a) => { acc = acc + a.v[1]; }, VArr(b) => { acc = acc + b.v[0]; } }
        i = i + 1;
    }
    return (acc - 5050) + __rc_underflow_count();
}`,
	},
	{
		// Uniform union whose struct payload ESCAPES (returned): the
		// enum is tainted (not free-eligible), so the variant struct
		// payload must NOT be deep-freed; the caller still holds it.
		name: "uniform_union_struct_escapes",
		src: `struct VInt { v: i32[] }
struct VArr { v: i32[] }
type Value = VInt | VArr;
function mk(n: i32): Value { return VArr { v: [n, n + 1, n + 2] }; }
function main(): i32 {
    var x: Value = mk(9);
    var c: i32 = 0;
    while (c < 200) {
        var junk: i32[] = [c, c];
        c = c + 1;
    }
    var got: i32 = 0;
    match (x) { VInt(a) => { got = a.v[1]; }, VArr(b) => { got = b.v[2]; } }
    return (got - 11) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation — a Map-typed STRUCT FIELD (the
		// headers-map shape). Dropping the owning struct now reclaims
		// the whole map structure (value column + buf + handle) via
		// __map_drop_values + __fern_map_drop, instead of the flat dec
		// that leaked it. Both helpers self-guard on the map's rc==1.
		// Churned 50x: an over-release would drift the counter.
		name: "struct_map_field_churn_free",
		src: `struct Req { headers: Map[string, string], n: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var m: Map[string, string] = map_new(8);
        m = m.set("k", "v");
        var r: Req = Req { headers: m, n: i };
        acc = acc + r.headers.get_or("k", "x").len();
        i = i + 1;
    }
    return (acc - 50) + __rc_underflow_count();
}`,
	},
	{
		// Map struct field that ESCAPES (struct returned): the map must
		// survive the constructor — the returned struct still owns it.
		// Churn forces same-size reuse that would corrupt a strayed read.
		name: "struct_map_field_escapes",
		src: `struct Req { headers: Map[string, string], n: i32 }
function mk(): Req {
    var m: Map[string, string] = map_new(8);
    m = m.set("k", "vv");
    return Req { headers: m, n: 1 };
}
function main(): i32 {
    var r: Req = mk();
    var c: i32 = 0;
    while (c < 100) {
        var junk: i32[] = [c, c];
        c = c + 1;
    }
    return (r.headers.get_or("k", "x").len() - 2) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation — GENERIC enum (Option[struct]). The
		// boxed Option[Item] carries a ParamType payload in its decl, so
		// the drop plan couldn't see the concrete type and flat-dec'd
		// (leaking the box + the Item). Substituting the type args
		// (Option[Item] → Some(Item)) recovers the concrete payload and
		// routes it through the tag-dispatch variant-plan deep-drop. Only
		// adopted when the substituted payload is a struct (so the
		// instantiation is heap-boxed, not pair-form). sum_{0..99}(i+1)=5050.
		name: "option_struct_payload_churn_free",
		src: `struct Item { tags: i32[] }
function mk(n: i32): Option[Item] { return Some(Item { tags: [n, n + 1] }); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var o: Option[Item] = mk(i);
        match (o) { Some(it) => { acc = acc + it.tags[1]; }, None => { acc = acc + 0; } }
        i = i + 1;
    }
    return (acc - 5050) + __rc_underflow_count();
}`,
	},
	{
		// Pair-form Option[i32] (scalar payload, no heap box) must be
		// untouched by the generic-enum substitution — it has no struct
		// payload, so it keeps the generic decl and the flat dec. A
		// stray box_free here would corrupt. sum_{0..99}(i) = 4950.
		name: "option_scalar_pairform_unaffected",
		src: `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var o: Option[i32] = Some(i);
        match (o) { Some(n) => { acc = acc + n; }, None => { acc = acc + 0; } }
        i = i + 1;
    }
    return (acc - 4950) + __rc_underflow_count();
}`,
	},
	{
		// Option[struct] that ESCAPES (returned): the box + payload must
		// survive the constructor; the caller still owns it. Churn forces
		// same-size reuse.
		name: "option_struct_escapes_return",
		src: `struct Item { tags: i32[] }
function mk(n: i32): Option[Item] { return Some(Item { tags: [n, n + 1] }); }
function main(): i32 {
    var o: Option[Item] = mk(7);
    var c: i32 = 0;
    while (c < 200) {
        var junk: i32[] = [c, c];
        c = c + 1;
    }
    var got: i32 = 0;
    match (o) { Some(it) => { got = it.tags[1]; }, None => { got = 0; } }
    return (got - 8) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation — a GENERIC enum as a struct FIELD
		// (`Holder { b: Option[Item] }`). Generic-enum LOCALS already
		// reclaim (the inline emitDec path substitutes the type args); a
		// nested field reaches the drop through __drop_struct_Holder →
		// dropFnNameFor, which previously bailed on any EnumType with Args
		// and flat-dec'd — leaking the Option box + Item + its buffer. It
		// now substitutes (Option[Item] → Some(Item)), confirms the
		// instantiation is heap-boxed (pointer payload), and routes to a
		// per-instantiation __drop_enum_<mangled> the worklist regenerates
		// from the stashed substituted decl. Churned 100x.
		// sum_{0..99}(i+1) = 5050.
		name: "generic_enum_struct_field_churn_free",
		src: `struct Item { xs: i32[] }
struct Holder { b: Option[Item], n: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var h: Holder = Holder { b: Some(Item { xs: [i, i + 1] }), n: i };
        match (h.b) { Some(it) => { acc = acc + it.xs[1]; }, None => { acc = acc + 0; } }
        i = i + 1;
    }
    return (acc - 5050) + __rc_underflow_count();
}`,
	},
	{
		// Generic-enum struct field that ESCAPES (Holder returned): the
		// Option box + Item + buffer must survive the constructor's exit
		// drop (the struct return-inc protects it). Churn forces same-size
		// reuse that would corrupt a strayed read after an over-release.
		name: "generic_enum_struct_field_escapes",
		src: `struct Item { xs: i32[] }
struct Holder { b: Option[Item], n: i32 }
function mk(n: i32): Holder { return Holder { b: Some(Item { xs: [n, n + 1] }), n: n }; }
function main(): i32 {
    var h: Holder = mk(9);
    var c: i32 = 0;
    while (c < 200) {
        var junk: i32[] = [c, c];
        c = c + 1;
    }
    var got: i32 = 0;
    match (h.b) { Some(it) => { got = it.xs[1]; }, None => { got = 0; } }
    return (got - 10) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation — array-of-struct as a struct FIELD
		// (the `struct Grid { rows: Row[] }` shape). Stage B deep-dropped
		// array-of-struct LOCALS; a nested field now routes through the
		// same __drop_arr_struct_<Elem> loop (each element box + buffer)
		// instead of the flat dec that leaked them. Churned 100x.
		// sum_{0..99}(i+1) = 5050.
		name: "struct_field_arr_of_struct_churn_free",
		src: `struct Row { cells: i32[] }
struct Grid { rows: Row[], tag: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var g: Grid = Grid { rows: [Row { cells: [i, i + 1] }, Row { cells: [i + 2] }], tag: i };
        acc = acc + g.rows[0].cells[1];
        i = i + 1;
    }
    return (acc - 5050) + __rc_underflow_count();
}`,
	},
	{
		// Array-of-struct field that ESCAPES (Grid returned): the rows
		// array + its Row boxes must survive the constructor. Churn
		// forces same-size reuse that would corrupt a strayed read.
		name: "struct_field_arr_of_struct_escapes",
		src: `struct Row { cells: i32[] }
struct Grid { rows: Row[], tag: i32 }
function mk(n: i32): Grid { return Grid { rows: [Row { cells: [n, n + 1] }], tag: n }; }
function main(): i32 {
    var g: Grid = mk(9);
    var c: i32 = 0;
    while (c < 200) {
        var junk: i32[] = [c, c];
        c = c + 1;
    }
    return (g.rows[0].cells[1] - 10) + (g.rows.len() - 1) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation — an ENUM-typed struct FIELD. A
		// `Holder { val: Value }` (Value = VInt | VArr) field flat-dec'd
		// and leaked the enum box + payload. It now routes through a
		// generated tag-dispatched __drop_enum_Value (reading the runtime
		// tag picks the exact per-variant payload type, then box_free's
		// with that variant's size). Churned 100x. sum_{0..99}(i+1)=5050.
		name: "struct_enum_field_churn_free",
		src: `struct VInt { v: i32[] }
struct VArr { v: i32[] }
type Value = VInt | VArr;
struct Holder { val: Value, n: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var h: Holder = Holder { val: VInt { v: [i, i + 1] }, n: i };
        match (h.val) { VInt(a) => { acc = acc + a.v[1]; }, VArr(b) => { acc = acc + b.v[0]; } }
        i = i + 1;
    }
    return (acc - 5050) + __rc_underflow_count();
}`,
	},
	{
		// Enum-typed field that ESCAPES (Holder returned): the enum box
		// + payload must survive the constructor. Churn forces reuse.
		name: "struct_enum_field_escapes",
		src: `struct VInt { v: i32[] }
struct VArr { v: i32[] }
type Value = VInt | VArr;
struct Holder { val: Value, n: i32 }
function mk(n: i32): Holder { return Holder { val: VArr { v: [n, n + 1] }, n: n }; }
function main(): i32 {
    var h: Holder = mk(9);
    var c: i32 = 0;
    while (c < 200) {
        var junk: i32[] = [c, c];
        c = c + 1;
    }
    var got: i32 = 0;
    match (h.val) { VInt(a) => { got = a.v[0]; }, VArr(b) => { got = b.v[1]; } }
    return (got - 10) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation — a PLAIN-array struct FIELD (i32[]).
		// Previously dropStructField only deep-dropped array-OF-struct
		// fields; a plain `data: i32[]` field flat-dec'd and leaked its
		// buffer. It now frees the buffer via __fern_arr_dec on the
		// owner's last reference (is_unique-gated). Churned 100x.
		// sum_{0..99}(i+1) = 5050.
		name: "struct_plain_array_field_churn_free",
		src: `struct Buf { data: i32[], n: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var b: Buf = Buf { data: [i, i + 1, i + 2], n: i };
        acc = acc + b.data[1];
        i = i + 1;
    }
    return (acc - 5050) + __rc_underflow_count();
}`,
	},
	{
		// Array-of-array struct field (i32[][]): the OUTER buffer is now
		// freed via __fern_drop_arr_ptr (inner buffers are array-of-array,
		// a later slice — still leak, no over-release). sum_{0..99}(i+1).
		name: "struct_arr_of_arr_field_churn_free",
		src: `struct Mat { rows: i32[][], n: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var m: Mat = Mat { rows: [[i, i + 1], [i + 2]], n: i };
        acc = acc + m.rows[0][1];
        i = i + 1;
    }
    return (acc - 5050) + __rc_underflow_count();
}`,
	},
	{
		// Plain-array field that ESCAPES (Buf returned): the buffer must
		// survive the constructor. Churn forces same-size reuse.
		name: "struct_plain_array_field_escapes",
		src: `struct Buf { data: i32[], n: i32 }
function mk(n: i32): Buf { return Buf { data: [n, n + 1, n + 2], n: n }; }
function main(): i32 {
    var b: Buf = mk(7);
    var c: i32 = 0;
    while (c < 200) {
        var junk: i32[] = [c, c];
        c = c + 1;
    }
    return (b.data[2] - 9) + (b.data.len() - 3) + __rc_underflow_count();
}`,
	},
	{
		// Transitive reclamation — enum with an ARRAY payload now frees
		// the array buffer. Widening the enum drop gate from
		// "struct payload" to "pointer-shaped payload" steers an
		// array-payload enum to the tag-dispatch variant-plan, whose arm
		// drops the i32[] via __fern_arr_dec (frees the buffer) instead
		// of the flat dec the uniform path used. Generic Option[i32[]],
		// churned. sum_{0..99}(i+1) = 5050.
		name: "option_array_payload_churn_free",
		src: `function mk(n: i32): Option[i32[]] { return Some([n, n + 1]); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var o: Option[i32[]] = mk(i);
        match (o) { Some(a) => { acc = acc + a[1]; }, None => { acc = acc + 0; } }
        i = i + 1;
    }
    return (acc - 5050) + __rc_underflow_count();
}`,
	},
	{
		// Uniform union with array payloads (E = A(i32[]) | B(i32[])):
		// previously the branchless uniform path flat-dec'd and leaked
		// the array buffer; now it steers to variant-plan and frees it
		// per tag-guarded arm. Churned.
		name: "uniform_enum_array_payload_churn_free",
		src: `enum E { A(i32[]), B(i32[]) }
function mk(n: i32): E { return A([n, n + 1]); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var e: E = mk(i);
        match (e) { A(a) => { acc = acc + a[1]; }, B(b) => { acc = acc + b[0]; } }
        i = i + 1;
    }
    return (acc - 5050) + __rc_underflow_count();
}`,
	},
	{
		// Option[array] that ESCAPES (returned): the box + array must
		// survive the constructor. Churn forces same-size reuse.
		name: "option_array_payload_escapes",
		src: `function mk(n: i32): Option[i32[]] { return Some([n, n + 1, n + 2]); }
function main(): i32 {
    var o: Option[i32[]] = mk(7);
    var c: i32 = 0;
    while (c < 200) {
        var junk: i32[] = [c, c];
        c = c + 1;
    }
    var got: i32 = 0;
    match (o) { Some(a) => { got = a[2]; }, None => { got = 0; } }
    return (got - 9) + __rc_underflow_count();
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
