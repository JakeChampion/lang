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
	// skipWasm names the tracking issue for a case whose wasm-backend rc
	// accounting is known-imbalanced (pre-existing; the case still gates
	// x86-64 + arm64). Empty = runs everywhere.
	skipWasm string
}{
	{
		// string[] whose elements alias a live local. Exercises the native
		// single-word x86-64 string[] element reclaim (__fern_drop_arr_str:
		// per-element __fern_str_dec then free the buffer); elements are
		// retained on store, so the per-element frees balance the shared
		// buffer. 3 × 4 = 12.
		name: "string_array_element_aliased",
		src: `
import "core/int";
import "std/string";
function cat(a: string, b: string): string { return a + b; }
function suml(arr: string[]): i32 { var s: i32 = 0; var i: i32 = 0; while (i < arr.len()) { s = s + arr[i].len(); i = i + 1; } return s; }
function main(): i32 {
    var s: string = cat("ab", "cd");
    var arr: string[] = [s, s];
    return (suml(arr) + s.len() - 12) + __rc_underflow_count();
}`,
	},
	{
		// String captured by a closure + kept live in the source local.
		// Exercises the native single-word x86-64 closure-capture string
		// reclaim (env drop → __fern_str_dec); the capture is retained at
		// MakeEnv (__fern_rc_inc), so the env's is_unique free balances the
		// shared buffer. 4 (via closure) + 4 (local) - 8 = 0.
		name: "string_closure_capture_aliased",
		src: `
import "core/int";
import "std/string";
function cat(a: string, b: string): string { return a + b; }
function callit(f: () => i32): i32 { return f(); }
function main(): i32 {
    var s: string = cat("ab", "cd");
    var f: () => i32 = () => s.len();
    var x: i32 = callit(f);
    var y: i32 = s.len();
    return (x + y - 8) + __rc_underflow_count();
}`,
	},
	{
		// String tuple element, aliased across two tuples + a live local.
		// Exercises the native single-word x86-64 tuple string-element
		// reclaim (__drop_tuple_<...> → __fern_str_dec); elements are
		// retained on tuple construction (__fern_rc_inc), so the per-tuple
		// frees balance the shared buffer to exactly one free. 3 × 4 = 12.
		name: "string_tuple_element_aliased",
		src: `
import "core/int";
import "std/string";
function cat(a: string, b: string): string { return a + b; }
function use2(p: (string, i32), q: (string, i32)): i32 { return p.0.len() + q.0.len(); }
function main(): i32 {
    var s: string = cat("ab", "cd");
    var a: (string, i32) = (s, 1);
    var b: (string, i32) = (s, 2);
    return (use2(a, b) + s.len() - 12) + __rc_underflow_count();
}`,
	},
	{
		// String enum payload (Option[string]), aliased across two values +
		// a live local. Exercises the native single-word enum payload
		// reclaim (appendChildDrop → __fern_str_dec); the payload is
		// retained on construction, so the frees balance. 3 × 4 = 12.
		name: "string_enum_payload_aliased",
		src: `
import "core/int";
import "std/string";
function cat(a: string, b: string): string { return a + b; }
function unwrap(o: Option[string]): i32 { match (o) { Some(v) => { return v.len(); }, None => { return 0; } } }
function main(): i32 {
    var s: string = cat("ab", "cd");
    var a: Option[string] = Some(s);
    var b: Option[string] = Some(s);
    return (unwrap(a) + unwrap(b) + s.len() - 12) + __rc_underflow_count();
}`,
	},
	{
		// Fresh string temp passed to a non-retaining call, in a loop.
		// Exercises the native single-word x86-64 owned-temp string drop
		// (emitOwnedSlotDrop call-arg path → __fern_str_dec): `a + "cd"` is
		// a fresh heap buffer (sole owner) consumed by `consume`, then freed
		// after the call so the loop does not leak. `a` is a param so the
		// concat is not constant-folded. 3 × len("abcd") = 12.
		name: "string_callarg_fresh_temp",
		src: `
import "core/int";
import "std/string";
function consume(s: string): i32 { return s.len(); }
function build(a: string): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3) { acc = acc + consume(a + "cd"); i = i + 1; }
    return acc;
}
function main(): i32 { return (build("ab") - 12) + __rc_underflow_count(); }`,
	},
	{
		// String struct field, aliased across two structs + a live local,
		// then all dropped. Exercises the native single-word x86-64
		// string-field reclaim (__drop_struct_<N> → __fern_str_dec): the
		// field-init alias-incs balance the per-struct frees so the shared
		// buffer is freed exactly once (no over-release). cat() defeats
		// constant-folding so the concat is a real heap string.
		name: "struct_string_field_aliased",
		src: `
import "core/int";
import "std/string";
struct H { name: string, n: i32 }
function cat(a: string, b: string): string { return a + b; }
function use2(p: H, q: H): i32 { return p.name.len() + q.name.len(); }
function main(): i32 {
    var s: string = cat("abc", "defgh");
    var a: H = H { name: s, n: 1 };
    var b: H = H { name: s, n: 2 };
    var total: i32 = use2(a, b) + s.len();
    return (total - 24) + __rc_underflow_count();
}`,
	},
	{
		// Array of structs: build, read back, drop at exit.
		name: "array_of_structs",
		src: `
import "core/int";
struct P { x: i32, y: i32 }
function main(): i32 {
    var ps: P[] = [P{x: 1, y: 2}, P{x: 3, y: 4}];
    return (ps[1].y - 4) + __rc_underflow_count();
}`,
	},
	{
		// Struct holding arrays, aliased then reassigned.
		name: "struct_of_arrays_aliased",
		src: `
import "core/int";
struct Holder { a: i32[], b: i32[] }
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
		src: `
import "core/int";
function main(): i32 {
    var cube: i32[][][] = [[[1, 2], [3]], [[4, 5, 6]]];
    return (cube[1][0][2] - 6) + __rc_underflow_count();
}`,
	},
	{
		// Union: build a variant, match it, drop at exit.
		name: "union_build_match",
		src: `
import "core/int";
import "std/string";
struct VInt { v: i32 }
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
		src: `
import "core/int";
import "std/string";
enum E { Arr(i32[]), Num(i32) }
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
		src: `
import "core/int";
function main(): i32 {
    var xs: i32[] = [5, 6, 7];
    var f = function (d: i32): i32 { return xs[2] + d; };
    var got: i32 = f(0);
    return (got - 7) + __rc_underflow_count();
}`,
	},
	{
		// Closure capturing a scalar (no pointer capture).
		name: "closure_capture_scalar",
		src: `
import "core/int";
function main(): i32 {
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
		src: `
import "core/int";
function main(): i32 {
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
		src: `
import "core/int";
function makeAdder(n: i32): (i32) => i32 {
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
		src: `
import "core/int";
function main(): i32 {
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
		src: `
import "core/int";
function main(): i32 {
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
		src: `
import "core/int";
import "core/map";
import "std/string";
function main(): i32 {
    var m: Map[string, string] = map_new(8);
    m = m.insert("hello", "world");
    m = m.insert("foo", "bar");
    var v: string = m.get_or("hello", "missing");
    return (v.len() - 5) + __rc_underflow_count();
}`,
	},
	{
		// O(N) push loop building an array of structs (push CoW +
		// dec-on-overwrite interplay).
		name: "push_loop_structs",
		src: `
import "core/int";
import "std/array";
import "std/string";
struct Node { id: i32 }
function main(): i32 {
    var ns: Node[] = [];
    var i: i32 = 0;
    while (i < 8) {
        ns = ns.append(Node { id: i });
        i = i + 1;
    }
    return (ns.len() - 8) + (ns[7].id - 7) + __rc_underflow_count();
}`,
	},
	{
		// Array-reassignment chain (dec-on-overwrite).
		name: "array_reassign_chain",
		src: `
import "core/int";
import "std/string";
function main(): i32 {
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
		src: `
import "core/int";
function sum3(a: i32[]): i32 { return a[0] + a[1] + a[2]; }
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
		src: `
import "core/int";
import "std/string";
struct Row { cells: i32[] }
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
		src: `
import "core/int";
struct P { x: i32 }
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
		src: `
import "core/int";
import "std/array";
import "std/string";
struct VInt { v: i32 }
struct VArr { v: i32[] }
type Value = VInt | VArr;
function vi(n: i32): Value { return VInt { v: n }; }
function va(xs: i32[]): Value { return VArr { v: xs }; }
function main(): i32 {
    var vs: Value[] = [];
    vs = vs.append(vi(1));
    vs = vs.append(va([2, 3]));
    vs = vs.append(vi(4));
    var got: i32 = 0;
    match (vs[1]) { VInt(n) => { got = n.v; }, VArr(a) => { got = a.v[1]; } }
    return (got - 3) + (vs.len() - 3) + __rc_underflow_count();
}`,
	},
	{
		// Struct holding both a union and an array.
		name: "struct_union_and_array",
		src: `
import "core/int";
import "std/string";
struct VInt { v: i32 }
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
		src: `
import "core/int";
struct S { v: i32 }
function main(): i32 {
    var s: S = S { v: 21 };
    var f = function (d: i32): i32 { return s.v + d; };
    return (f(0) - 21) + __rc_underflow_count();
}`,
	},
	{
		// Array of closures, each capturing the same array.
		name: "array_of_closures",
		src: `
import "core/int";
function main(): i32 {
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
		src: `
import "core/int";
import "std/string";
function pick(xs: i32[]): Option[i32[]] {
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
		src: `
import "core/int";
function main(): i32 {
    var t: (i32[], i32[]) = ([1, 2], [3, 4, 5]);
    return (t.0[1] - 2) + (t.1[2] - 5) + __rc_underflow_count();
}`,
	},
	{
		// String-local reclamation (wasm). A fresh concat result (always a
		// fresh headered heap buffer) in an owned local frees via
		// __fern_str_dec at scope exit. The aliased `s2 = s1` now retains
		// the shared buffer via the two-word __fern_str_inc, so both locals
		// reach the dec sweep; the rc==1 / is-unique gate frees once. Churned
		// 300x — a double-free / UAF / underflow on the freed concat
		// buffer would trip the checksum or the underflow detector. (On
		// native backends string reclamation is gated off; the checksum
		// is backend-independent.) s.len() = 2 each; 300*(2+2)=1200.
		name: "string_concat_local_churn_free",
		src: `
import "core/int";
import "std/string";
function mk(seed: i32): i32 {
    var pre: string = "v";
    var s: string = pre + "x";
    var n: i32 = s.len();
    var s2: string = s;
    return n + s2.len();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 300) { total = total + mk(k); k = k + 1; }
    return (total - 1200) + __rc_underflow_count();
}`,
	},
	{
		// String alias-inc stress (wasm). A single fresh concat buffer is
		// shared across THREE owned locals (s, s2, s3) — two alias-incs take
		// its rc to 3. At scope exit three __fern_str_dec calls fire; the
		// rc==1 / is-unique gate must decrement twice and free exactly once.
		// Over-counting (freeing while rc>1) corrupts the still-live aliases;
		// under-counting leaks. Churned 250x so any drift in the shared-buffer
		// rc accounting surfaces in the checksum or underflow detector. Each
		// .len() = 2; 250*(2+2+2)=1500.
		name: "string_alias_shared_buffer_churn_free",
		src: `function mk(seed: i32): i32 {
    var pre: string = "a";
    var s: string = pre + "b";
    var s2: string = s;
    var s3: string = s2;
    return s.len() + s2.len() + s3.len();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 250) { total = total + mk(k); k = k + 1; }
    return (total - 1500) + __rc_underflow_count();
}`,
	},
	{
		// String STRUCT FIELD reclamation (wasm). A struct holds a string
		// field initialised from a fresh concat aliased into the field
		// (__fern_str_inc on construction); at the struct local's last
		// reference its drop dec's the field (__fern_str_dec), freeing the
		// shared buffer once after the source local also decs. Churned
		// 300x — a double-free / UAF / underflow on the field buffer trips
		// the checksum or underflow detector. s.len()=2, h.name.len()=2;
		// 300*(2+2)=1200.
		name: "string_struct_field_churn_free",
		src: `struct Holder { name: string }
function mk(seed: i32): i32 {
    var pre: string = "v";
    var s: string = pre + "x";
    var h: Holder = Holder { name: s };
    return h.name.len() + s.len();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 300) { total = total + mk(k); k = k + 1; }
    return (total - 1200) + __rc_underflow_count();
}`,
	},
	{
		// String struct field from a LITERAL (wasm). The field is moved a
		// heap-form string literal (no construction inc); the struct drop's
		// __fern_str_dec must be a no-op on it (the 0x80000000 data-segment
		// sentinel header short-circuits), never freeing the immortal
		// literal. 200x churn; an over-release of the literal trips the
		// underflow detector. "a literal value" = 15 bytes.
		name: "string_struct_field_literal_churn",
		src: `struct Holder { name: string }
function mk(seed: i32): i32 {
    var h: Holder = Holder { name: "a literal value" };
    return h.name.len();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 3000) + __rc_underflow_count();
}`,
	},
	{
		// String struct field ESCAPES into an array, then the array's
		// array-of-struct deep drop (__drop_arr_struct_Holder →
		// __drop_struct_Holder) reclaims each struct's string field. 50
		// structs pushed; arr's last-reference drop frees every element box
		// + its field buffer. A missed / double field-free drifts the
		// checksum or underflow count. arr[10].name.len() = 2.
		name: "string_struct_field_escapes_into_array",
		src: `struct Holder { name: string }
function main(): i32 {
    var arr: Holder[] = [];
    var k: i32 = 0;
    while (k < 50) {
        var pre: string = "x";
        var s: string = pre + "y";
        arr = arr.append(Holder { name: s });
        k = k + 1;
    }
    var got: i32 = arr[10].name.len();
    return (got - 2) + __rc_underflow_count();
}`,
	},
	{
		// String TUPLE ELEMENT reclamation (wasm). A (string, i32) tuple's
		// string element is retained on construction (alias-inc), dup'd
		// when destructured (so the binding co-owns), and freed once by the
		// tuple's deep-drop __fern_str_dec after the binding + source also
		// dec. Exercises both projection paths (destructure `var (a,_)` and
		// direct `t.0`). 100x churn; a double-free / UAF / underflow on the
		// element buffer trips the checksum or underflow detector.
		// a.len()=2 + t.0.len()=2 + s.len()=2 = 6; 100*6=600.
		name: "string_tuple_elem_churn_free",
		src: `function mk(seed: i32): i32 {
    var pre: string = "v";
    var s: string = pre + "x";
    var t: (string, i32) = (s, seed);
    var (a, b) = t;
    return a.len() + t.0.len() + s.len();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 100) { total = total + mk(k); k = k + 1; }
    return (total - 600) + __rc_underflow_count();
}`,
	},
	{
		// String ARRAY ELEMENT reclamation (wasm). A string[] frees each
		// element string via the two-word walk in __fern_drop_arr_str
		// before returning the buffer, instead of leaking the elements
		// (buffer-only __fern_arr_dec). Built from a literal of two fresh
		// concats; 200x churn. A leaked / double-freed element buffer
		// drifts the checksum or trips the underflow detector.
		// arr[0].len()+arr[1].len() = 2+2 = 4; 200*4=800.
		name: "string_array_elem_churn_free",
		src: `function mk(seed: i32): i32 {
    var a: string = "a" + "x";
    var b: string = "b" + "y";
    var arr: string[] = [a, b];
    return arr[0].len() + arr[1].len();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 800) + __rc_underflow_count();
}`,
	},
	{
		// String[] grown by push then reclaimed: each pushed element moves
		// into the array (source escapes → not separately freed); the
		// array's __fern_drop_arr_str frees all of them at the last
		// reference. Exercises the push/grow + element-walk interaction.
		// 5 pushes/iter, arr[3].len()=2; 100x. 100*2=200.
		name: "string_array_push_churn_free",
		src: `function mk(seed: i32): i32 {
    var arr: string[] = [];
    var k: i32 = 0;
    while (k < 5) {
        var s: string = "v" + "x";
        arr = arr.append(s);
        k = k + 1;
    }
    return arr[3].len();
}
function main(): i32 {
    var total: i32 = 0;
    var j: i32 = 0;
    while (j < 100) { total = total + mk(j); j = j + 1; }
    return (total - 200) + __rc_underflow_count();
}`,
	},
	{
		// String ENUM PAYLOAD reclamation (wasm). A non-uniform enum with a
		// string payload variant: the eligible (inline / fresh payload)
		// enum local's tag-dispatched deep-drop dec's the string payload
		// via __fern_str_dec then frees the box. The match also extracts
		// the payload into a binding that OUTLIVES the enum (out = t), so
		// the binding co-owns and the buffer survives until both release.
		// 200x churn; "hello"+"world" = 10 each. 200*10=2000.
		name: "string_enum_payload_churn_free",
		src: `enum Msg { Text(string), Code(i32) }
function mk(seed: i32): i32 {
    var m: Msg = Text("hello" + "world");
    var out: string = "";
    match (m) { Text(t) => { out = t; }, Code(c) => { out = "z"; } }
    return out.len();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 2000) + __rc_underflow_count();
}`,
	},
	{
		// String CLOSURE CAPTURE reclamation (wasm). A closure captures a
		// fresh string; MakeClosure retains it (__fern_str_inc) and the
		// per-closure __closure_drop_<name> thunk dec's it (__fern_str_dec)
		// when the closure's env frees at its last reference. The closure
		// ESCAPES (returned from make_box), so the capture must survive
		// until the returned closure is dropped by the caller. 200x churn;
		// "ab"+"cd" = 4. 200*4=800.
		name: "string_closure_capture_churn_free",
		src: `function make_box(seed: i32): () => i32 {
    var s: string = "ab" + "cd";
    return function (): i32 { return s.len(); };
}
function mk(seed: i32): i32 {
    var f: () => i32 = make_box(seed);
    return f();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 800) + __rc_underflow_count();
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
		src: `
import "core/int";
function mk(seed: i32): i32 {
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
		src: `
import "core/int";
function main(): i32 {
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
		src: `
import "core/int";
function mk(n: i32): (i32, i32) { return (n, n + 1); }
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
		src: `
import "core/int";
function mk(seed: i32): i32 {
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
		src: `
import "core/int";
function mk(): i32[] {
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
		src: `
import "core/int";
function mk(seed: i32): i32 {
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
		src: `
import "core/int";
function mk(seed: i32): i32 {
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
		src: `
import "core/int";
function mk(k: i32): i32 {
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
		src: `
import "core/int";
import "core/map";
import "std/string";
function main(): i32 {
    var m: Map[i32, i32[]] = map_new(8);
    m = m.insert(1, [10, 20]);
    m = m.insert(2, [30, 40, 50]);
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
		src: `
import "core/int";
import "core/map";
struct Item { xs: i32[] }
function mk(seed: i32): i32 {
    var m: Map[i32, Item] = map_new(8);
    m = m.insert(seed, Item { xs: [seed, seed + 1] });
    m = m.insert(seed + 1, Item { xs: [seed + 2] });
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
		src: `
import "core/int";
import "core/map";
struct Item { xs: i32[] }
function mk(n: i32): Item {
    var m: Map[i32, Item] = map_new(4);
    m = m.insert(0, Item { xs: [n, n + 1] });
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
		// Map with ENUM values (valKind 4, generalized from struct). A
		// union value reclaims through __drop_map_via___drop_enum_Value
		// (deep-dropping each value's box + payload via the tag-dispatched
		// __drop_enum_Value) at the map's last reference, with set/get
		// retains balancing it. Churned 200x. got = a.v[1] = seed+1;
		// sum_{0..199}(k+1) = 20100.
		name: "map_enum_values_churn_free",
		src: `
import "core/int";
import "core/map";
struct VI { v: i32[] }
struct VA { v: i32[] }
type Value = VI | VA;
function mk(seed: i32): i32 {
    var m: Map[i32, Value] = map_new(8);
    m = m.insert(seed, VI { v: [seed, seed + 1] });
    var v: Value = m.get_or(seed, VA { v: [0] });
    var got: i32 = 0;
    match (v) { VI(a) => { got = a.v[1]; }, VA(b) => { got = b.v[0]; } }
    return got;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 20100) + __rc_underflow_count();
}`,
	},
	{
		// Map with ARRAY-OF-STRUCT values (Map[K, Item[]]). The value
		// array's buffer was freed (kind 3 drop_arr_ptr), but its Item
		// boxes + their xs buffers leaked. The value now deep-drops via
		// __drop_map_via___drop_arr_struct_Item → __drop_arr_struct_Item →
		// __drop_struct_Item per element. Churned 200x. vs[0].xs[1] =
		// seed+1; sum_{0..199}(k+1) = 20100.
		name: "map_arr_struct_values_churn_free",
		src: `
import "core/int";
import "core/map";
struct Item { xs: i32[] }
function mk(seed: i32): i32 {
    var m: Map[i32, Item[]] = map_new(8);
    m = m.insert(seed, [Item { xs: [seed, seed + 1] }, Item { xs: [seed + 2] }]);
    var vs: Item[] = m.get_or(seed, []);
    return vs[0].xs[1];
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 20100) + __rc_underflow_count();
}`,
	},
	{
		// Map with GENERIC-ENUM values (Map[K, Option[Item]], valKind 4 via
		// dropFnNameFor's generic path). The boxed instantiation reclaims
		// through __drop_map_via___drop_enum_Option_LB_Item_RB_ → the
		// mangled generic-enum drop (box + Item payload + xs buffer) at the
		// map's last reference, retained on set/get. Churned 200x. it.xs[1]
		// = seed+1; sum_{0..199}(k+1) = 20100.
		name: "map_generic_enum_values_churn_free",
		src: `
import "core/int";
import "core/map";
struct Item { xs: i32[] }
function mk(seed: i32): i32 {
    var m: Map[i32, Option[Item]] = map_new(8);
    m = m.insert(seed, Some(Item { xs: [seed, seed + 1] }));
    var o: Option[Item] = m.get_or(seed, None);
    var got: i32 = 0;
    match (o) { Some(it) => { got = it.xs[1]; }, None => { got = 0; } }
    return got;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 20100) + __rc_underflow_count();
}`,
	},
	{
		// Map OVERWRITE reclamation: re-`set`ting an existing key replaces
		// the value; for a struct value the IR pre-drops the old value via
		// __map_lookup_val + __drop_struct_Item (the runtime overwrite-dec
		// is a no-op for kind 4, so without this each replaced Item + its
		// xs buffer leaks). Three overwrites per key, churned 300x. The
		// live value is the LAST set: it.xs[0] = seed+4;
		// sum_{0..299}(k+4) = 44850 + 1200 = 46050.
		name: "map_overwrite_struct_churn_free",
		src: `
import "core/int";
import "core/map";
struct Item { xs: i32[] }
function mk(seed: i32): i32 {
    var m: Map[i32, Item] = map_new(8);
    m = m.insert(0, Item { xs: [seed, seed + 1] });
    m = m.insert(0, Item { xs: [seed + 2, seed + 3] });
    m = m.insert(0, Item { xs: [seed + 4] });
    var it: Item = m.get_or(0, Item { xs: [0] });
    return it.xs[0];
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 300) { total = total + mk(k); k = k + 1; }
    return (total - 46050) + __rc_underflow_count();
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
		src: `
import "core/int";
import "core/map";
import "std/string";
function add_pair(m: Map[i32, i32[]], k: i32): void {
    var arr: i32[] = [k * 10, k * 10 + 1];
    m = m.insert(k, arr);
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
		// Map[K, string] VALUE reclamation (wasm). String values are stored
		// boxed (an 8-byte (data, len) cell); the map's drop walks the value
		// column via __drop_map_str_values and __fern_str_dec's each value
		// buffer. set retains an aliased value, get retains the returned
		// string, and a key OVERWRITE pre-drops the replaced buffer. Here
		// key 1 is overwritten (firstval → secondvalue) and the surviving
		// value is gotten into an outliving binding. 200x churn; a double-
		// free / UAF / leak on a value buffer trips the checksum or the
		// underflow detector. out.len()=11 ("secondvalue"); 200*11=2200.
		name: "map_string_values_churn_free",
		src: `
import "core/int";
import "core/map";
import "std/string";
function mk(seed: i32): i32 {
    var m: Map[i32, string] = map_new(8);
    m = m.insert(1, "first" + "val");
    m = m.insert(2, "other" + "entry");
    m = m.insert(1, "second" + "value");
    var out: string = "";
    match (m.get(1)) { Some(v) => { out = v; }, None => { out = "z"; } }
    return out.len();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 2200) + __rc_underflow_count();
}`,
	},
	{
		// Map[string, V] KEY reclamation (wasm). String keys are stored
		// boxed (an 8-byte (data, len) cell), like values; the map's drop
		// walks the KEY column via __drop_map_str_keys and __fern_str_dec's
		// each key buffer. Here a Map[string, string] exercises BOTH column
		// walks at once, a fresh key (concat, moved) and an aliased key (the
		// `key` local, retained) are inserted, and the aliased key is
		// re-set (overwrite — the runtime keeps the existing key, discarding
		// the freshly-boxed one; the discarded key leaks but must not double
		// free). Both values are gotten back via match. Per iter: out.len()=5
		// ("value") + key.len()=3 ("key") + other.len()=3 ("xyz") = 11; 200x
		// churn → 2200. A double-free / UAF on a key or value buffer trips
		// the checksum or the underflow detector.
		name: "map_string_keys_churn_free",
		src: `
import "core/int";
import "core/map";
import "std/string";
function mk(seed: i32): i32 {
    var m: Map[string, string] = map_new(8);
    var key: string = "k" + "ey";
    m = m.insert(key, "va" + "lue");
    m = m.insert("other" + "k", "x" + "yz");
    m = m.insert(key, "val" + "ue");
    var out: string = "";
    match (m.get(key)) { Some(v) => { out = v; }, None => { out = "zz"; } }
    var other: string = "";
    match (m.get("otherk")) { Some(v) => { other = v; }, None => { other = "q"; } }
    return out.len() + key.len() + other.len();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 2200) + __rc_underflow_count();
}`,
	},
	{
		// Transient lookup-key reclamation (wasm). A string K on wasm32 is
		// boxed into a 16-byte cell for EVERY read-method call (get / has /
		// get_or) — the helper reads it for the strcmp but never retains
		// it, so the per-call cell leaked. Now each read frees its
		// transient key cell, and when the key was a FRESH owned temporary
		// (a concat, not an alias the caller still owns) its string buffer
		// is reclaimed too. Mixes aliased keys (the `key` local, which must
		// SURVIVE every lookup — a premature buffer dec would UAF the next
		// use) and fresh-concat keys (whose buffers must be freed exactly
		// once, no double free). Per iter: get_or(alias 7) + get_or(fresh 7)
		// + has(fresh 1) + has(alias 1) + get(fresh 7) + get(alias 7) +
		// key.len()=3 = 33; 500x → 16500. (delete has its own
		// case below — its result tuple now carries an rc header.)
		name: "map_string_lookup_key_churn_free",
		src: `
import "core/map";
import "std/string";
function mk(): i32 {
    var m: Map[string, i32] = map_new(8);
    var key: string = "ke" + "y";
    m = m.insert(key, 7);
    m = m.insert("ot" + "her", 3);
    var acc: i32 = 0;
    acc = acc + m.get_or(key, 0);
    acc = acc + m.get_or("ke" + "y", 0);
    if (m.has("ke" + "y")) { acc = acc + 1; }
    if (m.has(key)) { acc = acc + 1; }
    match (m.get("ke" + "y")) { Some(v) => { acc = acc + v; }, None => {} }
    match (m.get(key)) { Some(v) => { acc = acc + v; }, None => {} }
    return acc + key.len();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 16500) + __rc_underflow_count();
}`,
	},
	{
		// Map.delete returns a (Map, bool) tuple. The tuple box must
		// carry the 8-byte rc header every heap box gets (rc=1 at
		// [base+0], data = base+8) — without it the scope-exit tuple
		// drop reads heap-allocator metadata at [data-8] as the rc and
		// underflows (wasm) / corrupts the heap and segfaults (native).
		// Exercises every shape that hit the bug: bound result, the
		// `m = t.0` reassign idiom, delete-hit + delete-miss, and a
		// discarded delete — over both string and i32 keys. Per iter
		// the surviving "other"/2 entries stay readable: string side
		// +5 (hit 2 + survivor 3), i32 side +8 (hit 2 + survivor 6) →
		// 13; 500x → 6500.
		name: "map_delete_tuple_churn_free",
		src: `
import "core/int";
import "core/map";
import "std/string";
function mk(): i32 {
    var acc: i32 = 0;
    var sm: Map[string, i32] = map_new(8);
    sm = sm.insert("ke" + "y", 7);
    sm = sm.insert("ot" + "her", 3);
    sm = sm.insert("th" + "ird", 10);
    var st = sm.without("ke" + "y");   // bound delete-hit
    sm = st.0;                        // reassign idiom
    if (st.1) { acc = acc + 2; }
    sm = sm.without("th" + "ird").0;          // discarded delete (hit)
    var sm2 = sm.without("zz" + "zz"); // bound delete-miss
    sm = sm2.0;
    if (sm2.1) { acc = acc + 100; }
    acc = acc + sm.get_or("ot" + "her", 0); // surviving entry: +3
    var im: Map[i32, i32] = map_new(8);
    im = im.insert(1, 4);
    im = im.insert(2, 6);
    var it = im.without(1);            // i32 delete-hit
    im = it.0;
    if (it.1) { acc = acc + 2; }
    var im2 = im.without(9);           // i32 delete-miss
    im = im2.0;
    if (im2.1) { acc = acc + 100; }
    acc = acc + im.get_or(2, 0);      // surviving entry: +6
    return acc;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 6500) + __rc_underflow_count();
}`,
	},
	{
		// Map.get reboxes the helper's Option[usize] into a user-shaped
		// Option[V]. That box must carry the 8-byte rc header (rc=1 at
		// [base+0], data = base+8) like a Some(..) literal — without it
		// the scope-exit drop of an UNUSED `var o = m.get(k)` reads heap
		// metadata at [data-8] as the rc and underflows. Consumed /
		// discarded gets dodged it, so it stayed hidden. Exercises every
		// rebox arm left unused-and-dropped: Some + None, i32 / string
		// key, and a string VALUE (whose get-time __fern_str_inc must be
		// balanced by the unused Option's drop str_dec). One consumed
		// read anchors the value: +9 per iter; 500x → 4500.
		name: "map_get_option_header_churn_free",
		src: `
import "core/int";
import "core/map";
import "std/string";
function mk(): i32 {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 5);
    m = m.insert(2, 9);
    var hit = m.get(1);              // unused Some(i32), dropped at exit
    var miss = m.get(99);            // unused None, dropped at exit
    var sm: Map[string, i32] = map_new(8);
    sm = sm.insert("a" + "a", 3);
    var shit = sm.get("a" + "a");    // unused Some, string key
    var smiss = sm.get("z" + "z");   // unused None, string key
    var vm: Map[i32, string] = map_new(8);
    vm = vm.insert(1, "hi" + "there");
    var vhit = vm.get(1);            // unused Some(string): str_inc must balance
    var acc: i32 = 0;
    match (m.get(2)) { Some(x) => { acc = x; }, None => {} } // anchored read: 9
    return acc;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 4500) + __rc_underflow_count();
}`,
	},
	{
		// Map.keys() / .values() snapshot a column into a fresh array.
		// That array must use the standard 16-byte rc-array header
		// (capacity@data-12, rc=1@data-8, length@data-4, data@base+16)
		// — the i32 path (__map_column in core/map.fern) and the wide
		// i64/u64/f64 path (emitWideMapKeys/Values) both built a
		// length-only header, so the snapshot's scope-exit drop read
		// heap metadata at data-8 as the rc and underflowed (even when
		// consumed — the array drops at exit regardless). Exercises
		// unused + consumed snapshots on the i32 (runtime-helper) and
		// i64 (IR wide-builder) paths. Consumed i32 keys anchor the
		// value: len 3 + (1+2+3) = 9 per iter; 500x → 4500.
		name: "map_keys_values_header_churn_free",
		src: `
import "core/int";
import "core/map";
function mk(): i32 {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    m = m.insert(2, 20);
    m = m.insert(3, 30);
    var ks = m.keys();      // unused i32[] snapshot, dropped at exit
    var vs = m.values();    // unused i32[] snapshot, dropped at exit
    var wm: Map[i64, i64] = map_new(8);
    wm = wm.insert(5, 50);
    wm = wm.insert(6, 60);
    var wks = wm.keys();    // unused i64[] (IR wide path), dropped at exit
    var wvs = wm.values();  // unused i64[] (IR wide path), dropped at exit
    var ks2 = m.keys();     // consumed snapshot anchors the value
    return ks2.len() + ks2[0] + ks2[1] + ks2[2];
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 4500) + __rc_underflow_count();
}`,
	},
	{
		// String-keyed / string-valued keys()/values() go through
		// __map_string_column, which builds a `string[]` local and
		// returns `out as usize`. That cast severs the escape analysis,
		// so the rc pass dropped `out` at function exit even though it
		// escapes via the return — the caller then double-freed the
		// buffer (one underflow per snapshot call, regardless of entry
		// count). __map_string_column now retains the buffer once before
		// returning so the exit-drop balances. Long keys/values force
		// real heap strings (short strings inline with no buffer).
		// Exercises keys + values + string->string, unused + consumed.
		// Consumed anchors: ks.len()=2 + ks[0].len()=24 + ks[1].len()=24
		// = 50 per iter; 500x → 25000.
		name: "map_string_column_escape_churn_free",
		src: `
import "core/int";
import "core/map";
import "std/string";
function mk(): i32 {
    var m: Map[string, string] = map_new(8);
    m = m.insert("longkeyaaaaaaaaaaaaaaaa1", "longvalbbbbbbbbbbbbbbbb1");
    m = m.insert("longkeyaaaaaaaaaaaaaaaa2", "longvalbbbbbbbbbbbbbbbb2");
    var vs = m.values();    // unused string[] snapshot, dropped at exit
    var ks = m.keys();      // consumed snapshot anchors the value
    return ks.len() + ks[0].len() + ks[1].len();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 25000) + __rc_underflow_count();
}`,
	},
	{
		// Map iteration (for (k,v) in m / m.iter()) borrows entries from
		// the live buffer rather than snapshotting — __mapiter_key/value
		// hand out the raw stored pointer (value retained only for
		// array-valued maps). The regression-prone surface is a borrowed
		// key/value ESCAPING the loop into a container that outlives the
		// map: the rc pass must retain it at the store site, else the
		// container holds a borrowed ref the map's drop double-frees.
		// Exercises string keys + scalar values escaping via for-kv, and
		// array values escaping via the explicit iterator. Per iter:
		// keys_acc.len()=2 + vsum(10+20)=30 + outer[0][0]=100 = 132;
		// 500x → 66000.
		name: "map_iter_escape_churn_free",
		src: `
import "core/int";
import "core/map";
import "std/string";
function mk(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("longkeyaaaaaaaaaaaaaa1", 10);
    m = m.insert("longkeyaaaaaaaaaaaaaa2", 20);
    var keys_acc: string[] = [];
    var vsum: i32 = 0;
    for (k, v) in m { keys_acc = keys_acc.append(k); vsum = vsum + v; }
    var am: Map[i32, i32[]] = map_new(8);
    am = am.insert(1, [100, 200]);
    var outer: i32[][] = [];
    var it = am.iter();
    while (it.has_next()) { outer = outer.append(it.value()); it.advance(); }
    return keys_acc.len() + vsum + outer[0][0];
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 66000) + __rc_underflow_count();
}`,
	},
	{
		// Map[K, string] value reclamation across set / get / drop.
		// On wasm the values are stored as boxed (data, len) cells and
		// the generated __drop_map_str_values column walk str_decs each;
		// on natives the values are stored as direct data pointers and
		// the same generated walk rc_decs each (the prereq 1+2 L2 layout
		// gives every heap string an rc header at data-8 and every
		// literal a 0x80000000 sentinel, so the rc_dec is uniformly safe).
		// Set-retain bumps an aliased value's rc so the map co-owns it
		// alongside the source local; Map.get's returned Option co-owns
		// via the same retain mechanism (str_inc on wasm, rc_inc on
		// natives).
		//
		// Exercises: fresh value set (moves in, no inc), aliased value set
		// (inc'd), Map.get retain (Option keeps a live copy), drop reclaim
		// (column walk decs each entry). Long values force real heap
		// strings (short strings inline on wasm; natives have no inline).
		// Per iter: shared_len(33) + got_len(33) = 66; 500x → 33000.
		name: "map_string_value_reclaim_churn_free",
		src: `
import "core/int";
import "core/map";
import "std/string";
function mk(): i32 {
    var shared: string = "value-aaaaaaaaaaaaaaaaaaaa-shared";
    var m: Map[i32, string] = map_new(8);
    m = m.insert(1, shared);                              // aliased set
    m = m.insert(2, shared);                              // second alias
    m = m.insert(3, "value-bbbbbbbbbbbbbbbbbbbb-fresh"); // fresh value (moves in)
    var got: Option[string] = m.get(1);                // get retains
    var got_len: i32 = 0;
    match (got) { Some(s) => { got_len = s.len(); }, None => {} }
    return shared.len() + got_len;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 33000) + __rc_underflow_count();
}`,
	},
	{
		// Map[string, V] key reclamation — the symmetric slice to
		// map_string_value_reclaim_churn_free, for the KEY column. The
		// generated __drop_map_str_keys column walk reclaims each key's
		// string buffer at map drop. Set retains an aliased string key
		// so the column co-owns the buffer; fresh keys move in with
		// rc=1. An OVERWRITE with the same key keeps the original key
		// in place and discards the (freshly-boxed-on-wasm /
		// rc-inc-on-native) new key, which leaks the inc on wasm but is
		// bounded (accepted leak, documented in the plan).
		//
		// Coverage: aliased set + fresh set + overwrite + drop. Per iter:
		// key.len()=31; 500x → 15500.
		name: "map_string_key_reclaim_churn_free",
		src: `
import "core/int";
import "core/map";
import "std/string";
function mk(): i32 {
    var key: string = "key-aaaaaaaaaaaaaaaaaaaa-shared";
    var m: Map[string, i32] = map_new(8);
    m = m.insert(key, 1);                                // aliased key
    m = m.insert(key, 2);                                // re-set same key (overwrite)
    m = m.insert("key-bbbbbbbbbbbbbbbbbbbb-fresh", 3);   // fresh key (moves in)
    return key.len();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 15500) + __rc_underflow_count();
}`,
	},
	{
		// Inline-tagged strings (SSO ≤7 bytes) used as Map keys + values
		// on natives. A fresh slice / concat result with len ≤ 7 packs
		// into the "pointer" word with bit 0 set, so it's NOT a real heap
		// pointer. Without an inline-tag guard in __fern_rc_inc /
		// __fern_rc_dec the Map.set retain would deref the packed value
		// as if it were a pointer and corrupt unrelated heap memory
		// (segfault). Both Slice 7 (V retain) and Slice 8 (K retain) had
		// this latent crash; long-string corpus cases hid it because >7
		// chars take the heap path.
		//
		// Per iter: short_key.len()=1 + short_val.len()=2 = 3; 500x → 1500.
		name: "map_inline_string_kv_retain_no_crash",
		src: `
import "core/int";
import "core/map";
import "std/string";
function mk(): i32 {
    var src: string = "a=hi";
    var short_key: str = src[0:1];   // inline (1 byte, tagged)
    var short_val: str = src[2:4];   // inline (2 bytes, tagged)
    var m: Map[string, string] = map_new(8);
    m = m.insert(short_key, short_val);    // aliased inline K + V — retains must skip tagged
    var n = m.len();
    return short_key.len() + short_val.len();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 1500) + __rc_underflow_count();
}`,
	},
	{
		// Map.get_or returning a string V: the returned string must
		// survive the map drop. On wasm, emitWideMapGetOr now str_incs
		// the loaded (data, len) pair; on natives, the call dispatch
		// intercepts non-boxed string V and rc_incs the returned data
		// pointer. Without these, the caller would hold an un-retained
		// alias and the map drop's column walk would later free the
		// buffer → UAF when the caller eventually used the string.
		//
		// Per iter: v.len() = 28; 500x → 14000.
		name: "map_get_or_string_v_retain_churn_free",
		src: `
import "core/int";
import "core/map";
import "std/string";
function get_value(): string {
    var m: Map[i32, string] = map_new(8);
    m = m.insert(1, "value-aaaaaaaaaaaaaaaaaaaa-1");
    var v: string = m.get_or(1, "fallback-aaaaaaaaaaaaaaa");
    return v;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + get_value().len(); k = k + 1; }
    return (total - 14000) + __rc_underflow_count();
}`,
	},
	{
		// MapIter.value() / MapIter.key() returning a string K/V: the
		// returned string must survive the map drop. Each helper builds
		// a fresh map, iterates collecting strings into an outer array,
		// and returns the array — the map dies at scope exit; without
		// per-iter retain the column walk frees the strings the outer
		// array now holds (UAF). The wasm boxed branches str_inc the
		// (data, len); natives single-word rc_inc the returned data
		// pointer at the call dispatch.
		//
		// Per iter: vs[0].len()=28 + ks[0].len()=26 = 54; 300x → 16200.
		name: "map_iter_string_kv_retain_churn_free",
		src: `
import "core/int";
import "core/map";
import "std/string";
function collect_values(): string[] {
    var m: Map[i32, string] = map_new(8);
    m = m.insert(1, "value-aaaaaaaaaaaaaaaaaaaa-1");
    m = m.insert(2, "value-aaaaaaaaaaaaaaaaaaaa-2");
    var out: string[] = [];
    var it = m.iter();
    while (it.has_next()) { out = out.append(it.value()); it.advance(); }
    return out;
}
function collect_keys(): string[] {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("key-aaaaaaaaaaaaaaaaaaaa-1", 10);
    m = m.insert("key-aaaaaaaaaaaaaaaaaaaa-2", 20);
    var out: string[] = [];
    var it = m.iter();
    while (it.has_next()) { out = out.append(it.key()); it.advance(); }
    return out;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 300) {
        var vs = collect_values();
        var ks = collect_keys();
        total = total + vs[0].len() + ks[0].len();
        k = k + 1;
    }
    return (total - 16200) + __rc_underflow_count();
}`,
	},
	{
		// Map.set overwrite pre-drop for string V: replacing an existing
		// entry's value must reclaim the old buffer (the runtime's
		// type-erased overwrite-dec is a no-op for valKind 1). Without
		// the pre-drop, each overwrite leaks the old string buffer.
		// Wasm derefs the old cell and __fern_str_dec's it; natives load
		// the data pointer directly and __fern_rc_dec it. Three
		// overwrites per iter; 500x → 500 (just m.len() each iter, the
		// real check is uf=0 — proves the dec is balanced against the
		// drop walk, no over-release of the surviving value).
		name: "map_string_value_overwrite_pre_drop_churn",
		src: `
import "core/int";
import "core/map";
import "std/string";
function mk(): i32 {
    var m: Map[i32, string] = map_new(8);
    m = m.insert(1, "value-aaaaaaaaaaaaaaaaaaaa-A");
    m = m.insert(1, "value-aaaaaaaaaaaaaaaaaaaa-B");
    m = m.insert(1, "value-aaaaaaaaaaaaaaaaaaaa-C");
    return m.len();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 500) + __rc_underflow_count();
}`,
	},
	{
		// Slice 2 (string locals): a fresh string local from concat / slice
		// gets dec'd at scope exit on native single-word (x86_64) just
		// like wasm. Before Slice 2 lands on natives, the buffer leaked
		// every iteration; with the predicate enabled (needsRcIncOnAlias
		// + rcTracked + emitDec branch widened to ptrW=8 + !TwoWord),
		// each iter's fresh concat result frees through __fern_rc_dec
		// at scope exit. uf=0 + heap stays bounded confirms the reclaim.
		// (arm64 boxed excluded — no native str_dec runtime helper.)
		// Per iter: s.len() = 28; 500x → 14000.
		name: "native_string_local_concat_reclaim",
		src: `
import "core/int";
import "std/string";
function mk(): i32 {
    var s: string = "value-aaaaaaaaaaaaaaaaaaaa-" + "1";
    return s.len();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 14000) + __rc_underflow_count();
}`,
	},
	{
		// Slice 4 (string[] elements): a string[] local with FRESH element
		// values gets its buffer + each element dec'd at scope exit on
		// native single-word (x86_64) just like wasm. The wasm path goes
		// through __fern_drop_arr_str (two-word elements); on natives
		// the elements are single pointers so __fern_drop_arr_ptr does
		// the same job (walks the array, __fern_rc_dec's each element,
		// frees the buffer). SSO inline-tag low-bit guard in
		// __fern_rc_dec keeps short inline elements safe; literal
		// sentinel short-circuits on .LStr_N elements.
		// Per iter: 24 + 24 = 48; 500x → 24000.
		name: "native_string_array_local_reclaim",
		src: `
import "core/int";
import "std/string";
function mk(): i32 {
    var arr: string[] = ["value-aaaaaaaaaaaaaaaa-" + "1", "value-aaaaaaaaaaaaaaaa-" + "2"];
    return arr[0].len() + arr[1].len();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 24000) + __rc_underflow_count();
}`,
	},
	{
		// Slice 3 (string struct fields): a struct with a string field
		// dec's that field at scope exit on native single-word (x86_64),
		// mirroring wasm. The genStructDropFn body now loads the field
		// as WidthPtr (single ptr on natives) and __fern_rc_dec's it,
		// vs wasm's WidthString load + __fern_str_dec.
		// Per iter: name.len()=26 + count=7 = 33; 500x → 16500.
		name: "native_string_struct_field_reclaim",
		src: `
import "core/int";
import "std/string";
struct Item { name: string, count: i32 }
function mk(): i32 {
    var it: Item = Item { name: "value-aaaaaaaaaaaaaaaaaa-" + "1", count: 7 };
    return it.name.len() + it.count;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 16500) + __rc_underflow_count();
}`,
	},
	{
		// Slice 5 (string enum payloads): an enum variant carrying a
		// string payload reclaims the payload buffer on native single-word
		// (x86_64), mirroring wasm. The dropKind classifier now returns 4
		// for native single-word strings, so payloadDropLoadsFor /
		// enumVariantDropPlan include them; the existing decValueOnStack
		// / appendChildDrop emitters handle the actual __fern_rc_dec.
		// Per iter: s.len() = 26; 500x → 13000.
		name: "native_string_enum_payload_reclaim",
		src: `
import "core/int";
import "std/string";
enum Msg { Text(string), Number(i32) }
function mk(): i32 {
    var m: Msg = Msg.Text("value-aaaaaaaaaaaaaaaaaa-" + "1");
    match (m) {
        Text(s) => { return s.len(); },
        Number(n) => { return n; }
    }
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 13000) + __rc_underflow_count();
}`,
	},
	{
		// Slice 6 (string closure captures): a closure that captures a
		// string reclaims the capture from the env block on the closure's
		// last reference, on native single-word (x86_64). hasRcCapture now
		// counts string captures on natives, genClosureDropThunk emits
		// WidthPtr + __fern_rc_dec for them, and thunkSafe widens to
		// require the same MakeEnv inc invariant.
		// Per iter: s.len() = 26; 500x → 13000.
		name: "native_string_closure_capture_reclaim",
		src: `
import "core/int";
import "std/string";
function mk(): i32 {
    var s: string = "value-aaaaaaaaaaaaaaaaaa-" + "1";
    var f = function(): i32 { return s.len(); };
    return f();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 13000) + __rc_underflow_count();
}`,
	},
	{
		// Tuple sibling of Slice 3: a tuple with a string element dec's
		// the element at scope exit and dups it on destructure binding,
		// on native single-word (x86_64). Mirrors wasm's two-word path.
		// Per iter: s.len()=26 + n=7 = 33; 500x → 16500.
		name: "native_string_tuple_element_reclaim",
		src: `
import "core/int";
import "std/string";
function mk(): i32 {
    var t: (string, i32) = ("value-aaaaaaaaaaaaaaaaaa-" + "1", 7);
    var (s, n) = t;
    return s.len() + n;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 16500) + __rc_underflow_count();
}`,
	},
	{
		// Nested-tuple reclamation: ARRAY OF TUPLES (`(string,i32)[]`)
		// used to leak each tuple's string element — the array drop
		// fell through to the flat __fern_drop_arr_ptr which only
		// rc_dec's the element pointers (freeing each tuple box but
		// never traversing). arrElemStructDropName now also matches
		// tuple elements and emits a generated __drop_arr_tuple_<mangled>
		// loop that recurses through __drop_tuple_<mangled> per element
		// before freeing the buffer. Per iter: s.len()=26 + n=7 = 33;
		// 500x → 16500.
		name: "native_nested_tuple_string_in_array_reclaim",
		src: `
import "core/int";
import "std/string";
function mk(): i32 {
    var a: (string, i32)[] = [("value-aaaaaaaaaaaaaaaaaa-" + "1", 7)];
    return a[0].0.len() + a[0].1;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 16500) + __rc_underflow_count();
}`,
	},
	{
		// Nested-tuple reclamation: a tuple held inside an ENUM PAYLOAD
		// reaches the __drop_tuple_<mangled> helper through the
		// generated __drop_enum_<Wrap> body's appendChildDrop call,
		// closing the same string leak the struct-field case had. The
		// enum is wrapped in an outer struct so the variant-plan path
		// (worklist-driven) fires — a bare local hits the inline
		// fallback that can't safely route, a pre-existing orthogonal
		// eligibility-analysis gap. Per iter same arithmetic as the
		// sibling: 33 / iter, 500x → 16500.
		name: "native_nested_tuple_string_in_enum_reclaim",
		src: `
import "core/int";
import "std/string";
enum Wrap { Pair((string, i32)), Empty }
struct Holder { w: Wrap }
function mk(): i32 {
    var h: Holder = Holder { w: Pair(("value-aaaaaaaaaaaaaaaaaa-" + "1", 7)) };
    var r: i32 = 0;
    match (h.w) { Pair(q) => { r = q.0.len() + q.1; }, Empty => { r = 0; } }
    return r;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 16500) + __rc_underflow_count();
}`,
	},
	{
		// Nested-tuple reclamation: a tuple captured by a CLOSURE
		// reaches __drop_tuple_<mangled> through the generated
		// __closure_drop_<name> thunk, which dispatches captures via
		// dropFnNameFor (already did for arrays/structs/enums; post-fix
		// now also for tuples). Per iter: 33; 500x → 16500.
		name: "native_nested_tuple_string_in_closure_reclaim",
		src: `
import "core/int";
import "std/string";
function mk(): i32 {
    var p: (string, i32) = ("value-aaaaaaaaaaaaaaaaaa-" + "1", 7);
    var f: () => i32 = function(): i32 { return p.0.len() + p.1; };
    return f();
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 16500) + __rc_underflow_count();
}`,
	},
	{
		// Nested-tuple reclamation: a tuple held inside a STRUCT FIELD
		// used to leak its string element — the struct's drop dec'd the
		// tuple ptr flat (freeing the tuple box but never traversing the
		// elements). dropFnNameFor now routes the field through a
		// generated __drop_tuple_<mangled> helper which dec's the
		// string element + frees the tuple box at the struct's last
		// reference. Same routing fires for an enum-payload tuple, a
		// closure-capture tuple, or a tuple element that's itself a
		// tuple — the worklist is shape-driven, not container-driven.
		// Per iter: s.len()=26 + n=7 = 33; 500x → 16500.
		name: "native_nested_tuple_string_in_struct_reclaim",
		src: `
import "core/int";
import "std/string";
struct Box { items: (string, i32) }
function mk(): i32 {
    var b: Box = Box { items: ("value-aaaaaaaaaaaaaaaaaa-" + "1", 7) };
    return b.items.0.len() + b.items.1;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 16500) + __rc_underflow_count();
}`,
	},
	{
		// Escape into a struct field: an owned array stored in a
		// returned struct escapes the helper; freeing it at exit
		// would strand the field. Churn forces reuse of a freed block.
		name: "escape_array_into_struct_field",
		src: `
import "core/int";
import "std/string";
struct Box { items: i32[] }
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
		src: `
import "core/int";
import "std/string";
enum Wrap { Arr(i32[]), Empty }
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
		src: `
import "core/int";
import "std/array";
import "std/string";
function add_row(grid: i32[][], n: i32): i32[][] {
    var row: i32[] = [n, n + 1, n + 2, n + 3];
    return grid.append(row);
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
		src: `
import "core/int";
function main(): i32 {
    var row: i32[] = [6000, 6001, 6002, 6003];
    var grid: i32[][] = [[0, 0, 0, 0]];
    var grid2: i32[][] = grid.with(0, row);
    var c: i32 = 0;
    while (c < 300) {
        var junk: i32[] = [c, c, c, c];
        c = c + 1;
    }
    var r: i32[] = grid2[0];
    return (r[0] - 6000) + (r[3] - 6003) + __rc_underflow_count();
}`,
	},
	{
		// Escape into a struct field via struct-update
		// (`Box { ...b, items: arr }`): the new box stores the owned
		// `arr` (override-pointer-field path), and the old base box —
		// whose own `items` array is now unreferenced — is dropped.
		// The escaped array must survive the helper's exit, and the
		// base's array must be freed exactly once.
		name: "escape_array_into_struct_update",
		src: `
import "core/int";
struct Box { items: i32[] }
function fill(b: Box, n: i32): Box {
    var arr: i32[] = [n, n + 1, n + 2, n + 3];
    return Box { ...b, items: arr };
}
function main(): i32 {
    var b: Box = Box { items: [0, 0, 0, 0] };
    b = fill(b, 7000);
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
		src: `
import "core/int";
import "core/map";
function build(seed: i32): i32 {
    var m: Map[i32, i32] = map_new(8);
    var i: i32 = 0;
    while (i < 16) { m = m.insert(i, seed + i); i = i + 1; }
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
		src: `
import "core/int";
import "core/map";
function make_map(n: i32): Map[i32, i32] {
    var m: Map[i32, i32] = map_new(4);
    m = m.insert(1, n);
    m = m.insert(2, n * 2);
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
		src: `
import "core/int";
struct Pair { a: i32, b: i32 }
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
		src: `
import "core/int";
struct Holder { xs: i32[], n: i32 }
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
		src: `
import "core/int";
enum Wrap { A(i32[]), B(i32[]) }
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
		src: `
import "core/int";
enum Wrap { A(i32[]), B(i32[]) }
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
		src: `
import "core/int";
enum V { I(i32), A(i32[]) }
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
		src: `
import "core/int";
import "core/map";
import "std/string";
import "std/url";
function main(): i32 {
    var bad: i32 = 0;
    var m: Map[string, string[]] = url.query_parse("a=1&b=2&tag=x&tag=y");
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
		src: `
import "core/int";
import "std/json";
import "std/string";
function main(): i32 {
    var bad: i32 = 0;
    match (json.json_parse("{\"a\":[1,2,3],\"b\":\"hi\"}")) {
        Some(v) => {
            match (v) {
                JObject(m) => { if (m.len() != 2) { bad = bad + 1; } },
                _ => { bad = bad + 10; }
            }
        },
        None => { bad = bad + 100; }
    }
    var arr: JsonValue[] = [JNumber("1"), JBool(true)];
    if (json.json_encode(JArray(arr)) != "[1,true]") { bad = bad + 1000; }
    return bad + __rc_underflow_count();
}`,
	},
	{
		// Cursor-idiom coverage: the json parser threads an immutable
		// __JsonParser cursor as a (result, cursor) tuple through 8
		// mutually-recursive functions (struct-update advance, no field
		// mutation). This case drives every cursor-threading path —
		// numbers, \uXXXX escapes (__json_p_uhex through the cursor),
		// nested arrays + objects (recursion), and a malformed input
		// that must thread error:1 back through the cursor to None.
		// RC-gated: the parser copies each borrowed `p` param into a
		// local before struct-updating it in a loop, so the underflow
		// counter must stay 0 even under free-on.
		name: "stdlib_json_cursor_idiom",
		src: `
import "core/int";
import "std/json";
import "std/string";
function main(): i32 {
    var bad: i32 = 0;
    // nested arrays + objects + \uXXXX escape round-trip
    match (json.json_parse("{\"nums\":[-1,2.5,3e2],\"k\":\"a\\u0041b\",\"o\":{\"d\":[true,null,false]}}")) {
        Some(v) => {
            match (json.json_get_string(v, "k")) {
                Some(s) => { if (s != "aAb") { bad = bad + 1; } },
                None => { bad = bad + 2; }
            }
            match (json.json_get_array(v, "nums")) {
                Some(a) => { if (a.len() != 3) { bad = bad + 4; } },
                None => { bad = bad + 8; }
            }
        },
        None => { bad = bad + 100; }
    }
    // malformed: error threads through the cursor → None
    match (json.json_parse("{\"a\":}")) {
        Some(v) => { bad = bad + 1000; },
        None => { }
    }
    match (json.json_parse("[1,2")) {
        Some(v) => { bad = bad + 2000; },
        None => { }
    }
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
		src: `
import "core/int";
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    var i: i32 = 0;
    while (i < 100) { m = m.insert(i, i * 2); i = i + 1; }
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
		src: `
import "core/int";
import "core/map";
function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    m = m.insert(1, [10, 20, 30]);
    m = m.insert(2, [40, 50]);
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
		src: `
import "core/int";
import "core/map";
import "std/string";
function main(): i32 {
    var m: Map[string, string[]] = map_new(4);
    m = m.insert("a", ["x", "yy", "zzz"]);
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
		src: `
import "core/int";
import "core/map";
import "std/string";
function main(): i32 {
    var m: Map[i32, i32[][]] = map_new(4);
    m = m.insert(1, [[1, 2], [3, 4, 5]]);
    var v: i32[][] = m.get_or(1, []);
    return (v[1].len() + v[0][1] - 5) + __rc_underflow_count();
}`,
	},
	{
		// Aliased array value: the set value is an Ident
		// (needsRcIncOnAlias → inc-on-set), so the source local's
		// exit dec and the map's drop balance to a single free.
		name: "map_aliased_array_value",
		src: `
import "core/int";
import "core/map";
function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    var arr: i32[] = [7, 8, 9];
    m = m.insert(5, arr);
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
		src: `
import "core/int";
import "core/map";
function lookup(m: Map[i32, i32[]], k: i32): i32[] {
    return m.get_or(k, []);
}
function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    m = m.insert(1, [100, 200]);
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
		src: `
import "core/int";
import "core/map";
import "std/array";
import "std/string";
function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    m = m.insert(1, [10]);
    match (m.get(1)) {
        Some(cur) => { m = m.insert(1, cur.append(20)); },
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
		src: `
import "core/int";
import "core/map";
function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    var i: i32 = 0;
    while (i < 200) { m = m.insert(7, [i, i + 1, i + 2]); i = i + 1; }
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
		src: `
import "core/int";
import "core/map";
function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    m = m.insert(1, [10, 11]);
    var borrow: i32[] = m.get_or(1, []);
    m = m.insert(1, [20, 21]);
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
		src: `
import "core/int";
import "core/map";
import "std/string";
function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    m = m.insert(1, [3, 4]);
    m = m.insert(2, [5, 6, 7]);
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
		src: `
import "core/int";
import "core/map";
import "std/string";
function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    m = m.insert(1, [3, 4]);
    m = m.insert(2, [5, 6, 7]);
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
		src: `
import "core/int";
struct Inner { vals: i32[] }
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
		src: `
import "core/int";
import "core/int";
struct VInt { v: i32[] }
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
		src: `
import "core/int";
struct Inner { vals: i32[] }
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
		src: `
import "core/int";
struct Inner { vals: i32[] }
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
		src: `
import "core/int";
import "std/array";
import "std/string";
struct Inner { vals: i32[] }
struct Outer { inner: Inner }
function main(): i32 {
    var shared: Inner = Inner { vals: [1, 2, 3] };
    var keep: Outer[] = [];
    var o: Outer = Outer { inner: shared };
    keep = keep.append(o);
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
		src: `
import "core/int";
struct Item { tags: i32[] }
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
		src: `
import "core/int";
struct P { x: i32 }
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
		src: `
import "core/int";
import "std/string";
struct Item { tags: i32[] }
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
		src: `
import "core/int";
struct Pt { xs: i32[], y: i32 }
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
		src: `
import "core/int";
struct Pt { xs: i32[], y: i32 }
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
		src: `
import "core/int";
function pick_arr(o: Option[i32], a0: i32[]): i32[] {
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
		src: `
import "core/int";
struct Item { tags: i32[] }
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
		src: `
import "core/int";
import "std/array";
import "std/string";
struct Item { tags: i32[] }
enum Node { Leaf(Item), Branch(i32) }
function main(): i32 {
    var keep: Node[] = [];
    keep = keep.append(Leaf(Item { tags: [5, 6] }));
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
		src: `
import "core/int";
struct Pt { x: i32, y: i32 }
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
		src: `
import "core/int";
struct Pt { x: i32, y: i32 }
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
		src: `
import "core/int";
struct Item { tags: i32[] }
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
		src: `
import "core/int";
import "std/string";
struct Item { tags: i32[] }
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
		src: `
import "core/int";
import "core/int";
struct VInt { v: i32[] }
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
		src: `
import "core/int";
struct VInt { v: i32[] }
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
		src: `
import "core/int";
import "core/map";
import "std/string";
struct Req { headers: Map[string, string], n: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var m: Map[string, string] = map_new(8);
        m = m.insert("k", "v");
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
		src: `
import "core/int";
import "core/map";
import "std/string";
struct Req { headers: Map[string, string], n: i32 }
function mk(): Req {
    var m: Map[string, string] = map_new(8);
    m = m.insert("k", "vv");
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
		src: `
import "core/int";
struct Item { tags: i32[] }
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
		src: `
import "core/int";
function main(): i32 {
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
		src: `
import "core/int";
struct Item { tags: i32[] }
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
		src: `
import "core/int";
struct Item { xs: i32[] }
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
		src: `
import "core/int";
struct Item { xs: i32[] }
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
		src: `
import "core/int";
struct Row { cells: i32[] }
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
		src: `
import "core/int";
import "std/string";
struct Row { cells: i32[] }
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
		src: `
import "core/int";
struct VInt { v: i32[] }
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
		src: `
import "core/int";
struct VInt { v: i32[] }
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
		src: `
import "core/int";
struct Buf { data: i32[], n: i32 }
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
		src: `
import "core/int";
struct Mat { rows: i32[][], n: i32 }
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
		src: `
import "core/int";
import "std/string";
struct Buf { data: i32[], n: i32 }
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
		src: `
import "core/int";
function mk(n: i32): Option[i32[]] { return Some([n, n + 1]); }
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
		src: `
import "core/int";
enum E { A(i32[]), B(i32[]) }
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
		src: `
import "core/int";
function mk(n: i32): Option[i32[]] { return Some([n, n + 1, n + 2]); }
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
	{
		// Cell[i32] churn: a fresh cell per iteration, freed at loop-body
		// scope exit so the next alloc reuses the block. A cell is a
		// one-element ARRAY box (16-byte header), so its reclaim must go
		// through __fern_arr_dec (base = data-16), NOT the struct box_free
		// (base = data-8) — the latter mis-frees the header and corrupts the
		// freelist over the churn, drifting `acc`. sum of (i+1), i in 0..199
		// = sum 1..200 = 20100.
		name: "cell_i32_churn",
		src: `
import "core/int";
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var c = cell_new(i);
        c.set(c.get() + 1);
        acc = acc + c.get();
        i = i + 1;
    }
    return (acc - 20100) + __rc_underflow_count();
}`,
	},
	{
		// Cell[string] full rc cycle with HEAP strings: cell_new moves a
		// fresh concat in; set OVERWRITES (pre-drops the old heap buffer,
		// moves the new one in); get RETAINS the returned string into a
		// local that drops at scope exit; the cell's own drop reclaims its
		// slot buffer + box via __fern_drop_arr_str (wasm/arm64) /
		// __fern_drop_arr_ptr (x86_64). Churned 100x so any unbalanced
		// retain/release drifts the underflow counter (over-release) — a
		// leak would read 0 too, but the buffers here are all reclaimed.
		// "y0".."y9" len 2 (10) + "y10".."y99" len 3 (90) = 20 + 270 = 290.
		name: "cell_string_overwrite_churn",
		src: `
import "std/string";
function main(): i32 {
    var n: i32 = 0;
    var base: string = "hello";
    var i: i32 = 0;
    while (i < 100) {
        var c = cell_new(base + "_a");
        c.set(base + "_bb");
        var s = c.get();
        n = n + s.len();
        i = i + 1;
    }
    return (n - 800) + __rc_underflow_count();
}`,
	},
	{
		// `a[0].with(0, 9)` on a NESTED array: the receiver `a[0]` is a
		// projection out of the live outer array `a`, i.e. a BORROW — the inner
		// buffer is still owned by `a`, so an in-place cow would corrupt `a[0]`.
		// computeArraySetIncs previously treated any non-ident receiver as a
		// dead fresh temp and skipped the inc, so native mutated `a[0]` in place
		// (`a[0][0]` became 9) → 9 + 9 = 18 instead of the copy-on-write 9 + 1
		// = 10 (interp/self-host were already correct). The fix forces the inc
		// for an Index/FieldAccess/Slice receiver so cow copies. r[0]=9,
		// a[0][0]=1 → 10; the -10 makes a correct run read 0.
		name: "nested_array_with_borrowed_element",
		src: `
function main(): i32 {
    var a: i32[][] = [[1, 2], [3, 4]];
    var r: i32[] = a[0].with(0, 9);
    return (r[0] + a[0][0] - 10) + __rc_underflow_count();
}`,
	},
	{
		// Same borrow hazard through a STRUCT FIELD: `s.xs.with(0, 9)` must not
		// mutate `s.xs` in place. r[0]=9, s.xs[0]=1 → 10; -10 → 0 when correct.
		name: "struct_field_array_with_borrowed",
		src: `
struct S { xs: i32[] }
function main(): i32 {
    var s: S = S { xs: [1, 2, 3] };
    var r: i32[] = s.xs.with(0, 9);
    return (r[0] + s.xs[0] - 10) + __rc_underflow_count();
}`,
	},
	{
		// `.with` on a BORROWED parameter: `xs` is a non-`own`, borrow-inferred
		// param (the callee only reads it), so the caller still owns the buffer
		// (rc 1, the caller's) — an in-place cow would mutate the caller's array.
		// computeArraySetIncs treated the param at its last use as a move and
		// skipped the inc, so native mutated the caller's `a` in place
		// (`a[0]` became 99) → 99 + 99 = 198 instead of the copy-on-write
		// 99 + 1 = 100. The fix forces the inc for a borrowed-param receiver.
		// b[0]=99, a[0]=1 → 100; the -100 makes a correct run read 0.
		name: "with_on_borrowed_param",
		src: `
function bump(xs: i32[]): i32[] { return xs.with(0, 99); }
function main(): i32 {
    var a: i32[] = [1, 2];
    var b: i32[] = bump(a);
    return (b[0] + a[0] - 100) + __rc_underflow_count();
}`,
	},
	{
		// Same borrow hazard via reassign-to-self on the param (`xs = xs.with`):
		// the local rebind is fine but the buffer is still the caller's, so it
		// must copy, not mutate in place. 99 + 1 - 100 → 0 when correct.
		name: "with_reassign_self_borrowed_param",
		src: `
function bump(xs: i32[]): i32[] { xs = xs.with(0, 99); return xs; }
function main(): i32 {
    var a: i32[] = [1, 2];
    var b: i32[] = bump(a);
    return (b[0] + a[0] - 100) + __rc_underflow_count();
}`,
	},
	{
		// `.with` on an array of rc-tracked STRUCT elements through a functional
		// record update, reassigned in a loop (`r = upd(r, i, ...)`). Each upd
		// copies r.ops (the receiver stays live, forcing the CoW copy branch) and
		// replaces one element. The copy shares the receiver's struct-pointer
		// elements; the plain __fern_arr_cow_inplace memcpy'd them WITHOUT an inc,
		// so when the previous r was dropped it freed the elements the new r still
		// referenced — a use-after-free that recycled the boxes mid-loop and
		// corrupted later reads (native returned the wrong sum). The
		// pointer-aware __fern_arr_cow_inplace_ptr inc's each copied element so
		// both arrays own it. sum = 3*(0+..+39) = 2340; -2340 → 0 when correct.
		name: "with_struct_elem_functional_update_loop",
		src: `
struct Op2 { a: i32, c: i32 }
struct R { ops: Op2[], p: i32 }
function upd(r: R, i: i32, v: i32): R {
    return R { ops: r.ops.with(i, Op2 { a: v, c: 7 }), p: r.p };
}
function main(): i32 {
    var ops: Op2[] = [];
    var k: i32 = 0;
    while (k < 40) { ops = ops.append(Op2 { a: 0, c: 0 }); k = k + 1; }
    var r: R = R { ops: ops, p: 0 };
    var i: i32 = 0;
    while (i < 40) { r = upd(r, i, i * 3); i = i + 1; }
    var s: i32 = 0;
    var j: i32 = 0;
    while (j < 40) { s = s + r.ops[j].a; j = j + 1; }
    return (s - 2340) + __rc_underflow_count();
}`,
	},
	{
		// `.with` on an array of struct elements where the receiver is a bare
		// local that stays LIVE after the call (`b = a.with(0, ..)`; a read
		// again), so the CoW copy branch fires. The old element being overwritten
		// at index 0 is dropped, the rest are inc'd on the copy; a churn loop
		// recycles any freed block to surface a stale-pointer read. a stays
		// {10,20,30}, b is {99,20,30}. All four deltas 0 → 0 when correct.
		name: "with_struct_elem_bare_ident_forced_copy",
		src: `
struct Op2 { a: i32, c: i32 }
function main(): i32 {
    var a: Op2[] = [Op2 { a: 10, c: 0 }, Op2 { a: 20, c: 0 }, Op2 { a: 30, c: 0 }];
    var b: Op2[] = a.with(0, Op2 { a: 99, c: 0 });
    var i: i32 = 0;
    while (i < 30) { var z: Op2[] = [Op2 { a: i, c: i }]; i = i + 1; }
    return (a[0].a - 10) + (a[1].a - 20) + (b[0].a - 99) + (b[1].a - 20) + __rc_underflow_count();
}`,
	},
	{
		// `.with` functional-update loop where the struct element carries a
		// STRING field. On the CoW copy branch the old element being replaced is
		// deep-dropped (__drop_struct_ frees its string when it is the last
		// reference) and the carried elements are inc'd — so the string field's
		// rc stays balanced (no double-free of "x"/"y", no over-release). sum =
		// 0+1+..+29 = 435; -435 → 0 when correct.
		name: "with_struct_string_elem_functional_update_loop",
		src: `
struct Op { a: i32, b: string }
struct R { ops: Op[], p: i32 }
function upd(r: R, i: i32, v: i32): R {
    return R { ops: r.ops.with(i, Op { a: v, b: "y" }), p: r.p };
}
function main(): i32 {
    var ops: Op[] = [];
    var k: i32 = 0;
    while (k < 30) { ops = ops.append(Op { a: 0, b: "x" }); k = k + 1; }
    var r: R = R { ops: ops, p: 0 };
    var i: i32 = 0;
    while (i < 30) { r = upd(r, i, i); i = i + 1; }
    var s: i32 = 0;
    var j: i32 = 0;
    while (j < 30) { s = s + r.ops[j].a; j = j + 1; }
    return (s - 435) + __rc_underflow_count();
}`,
	},
	{
		// `.with` on an array-of-ARRAYS (rc-tracked pointer elements are the inner
		// buffers), receiver live after the call → CoW copy branch. Exercises the
		// ArrayType element arm of __fern_arr_cow_inplace_ptr / the old-element
		// drop. g stays {[1,1],[2,2],[3,3]}, h is {[9,9],[2,2],[3,3]}. 0 when
		// correct.
		name: "with_array_elem_forced_copy",
		src: `
function main(): i32 {
    var g: i32[][] = [[1, 1], [2, 2], [3, 3]];
    var h: i32[][] = g.with(0, [9, 9]);
    var i: i32 = 0;
    while (i < 30) { var z: i32[] = [i, i, i]; i = i + 1; }
    return (g[0][0] - 1) + (g[1][0] - 2) + (h[0][0] - 9) + (h[1][0] - 2) + __rc_underflow_count();
}`,
	},
	{
		// string[] grown through repeated `.append` inside a struct functional
		// update (`S{f: s.f.append(v)}` — the EmitState.needed threading shape,
		// #3425). Each append's grow COPY duplicates the element pointers; the
		// old struct's drop then walk-frees its buffer at rc==1
		// (__fern_drop_arr_str), so without the __fern_arr_push_grow_ptr/_str
		// element retain the copied elements were freed under the live grown
		// buffer — the self-host-driver SIGSEGV / poison-mode UAF. 40 elements
		// force multiple grows; every element's length + prefix is verified
		// after all the intermediate structs have been dropped. 0 when correct.
		name: "string_array_append_grow_struct_field",
		src: `
struct St {
    needed: string[],
    n: i32
}
function need(s: St, name: string): St {
    return St { needed: s.needed.append(name), n: s.n + 1 };
}
function main(): i32 {
    var s: St = St { needed: [], n: 0 };
    var base: string = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMN";
    var i: i32 = 0;
    while (i < 40) {
        var piece: str = base[0 : 20 + (i - (i / 30) * 30)];
        s = need(s, "structdroptestprefix:" + piece);
        i = i + 1;
    }
    var bad: i32 = 0;
    var j: i32 = 0;
    while (j < 40) {
        var want: i32 = 21 + 20 + (j - (j / 30) * 30);
        if (s.needed[j].len() != want) { bad = bad + 1; }
        if (s.needed[j][0:20] != "structdroptestprefix") { bad = bad + 1; }
        j = j + 1;
    }
    return bad + __rc_underflow_count();
}`,
	},
	{
		// struct[] grown through repeated `.append` inside a struct functional
		// update — the parser `Par{toks: Token[]}` shape (#3425). Same grow-copy
		// element-share as the string[] case, but the freeing walk is the deep
		// struct-element drop (__drop_arr_struct_<E> / element arr_dec), which
		// freed the shared Tok boxes (and their string fields) under the live
		// grown buffer. Pre-fix this segfaulted outright on x86-64. 0 when
		// correct.
		name: "struct_array_append_grow_struct_field",
		src: `
struct Tok {
    kind: i32,
    text: string
}
struct Par {
    toks: Tok[],
    pos: i32
}
function push_tok(p: Par, t: Tok): Par {
    return Par { toks: p.toks.append(t), pos: p.pos + 1 };
}
function main(): i32 {
    var p: Par = Par { toks: [], pos: 0 };
    var base: string = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMN";
    var i: i32 = 0;
    while (i < 40) {
        var t: Tok = Tok { kind: i, text: "tokentextprefixvalue:" + base[0 : 20 + (i - (i / 30) * 30)] };
        p = push_tok(p, t);
        i = i + 1;
    }
    var bad: i32 = 0;
    var j: i32 = 0;
    while (j < 40) {
        if (p.toks[j].kind != j) { bad = bad + 1; }
        var want: i32 = 21 + 20 + (j - (j / 30) * 30);
        if (p.toks[j].text.len() != want) { bad = bad + 1; }
        j = j + 1;
    }
    return bad + __rc_underflow_count();
}`,
	},
	{
		// Recursive `(T, Cur)` tuple reader threading a SCALAR-ONLY struct
		// cursor with `c = c2;` in a loop. Borrow inference demotes the
		// non-escaping `c` param; before the computeConsumedParams fix the
		// reassignment's overwrite dec released the caller's reference, the
		// caller's destructure-temp deep drop then freed the cursor box
		// early, and the still-live aliases double-freed it — freelist
		// corruption that crashed __fern_alloc at recursion depth 4.
		// Scalar-only cursors were the exact gap: string/array-bearing ones
		// (json.fern's __JsonParser) were already consumed-promoted.
		// 3^5 = 243 per build.
		name: "tuple_return_scalar_cursor_recursion",
		src: `
struct Leaf { x: i32 }
struct Node { kids: T[] }
type T = Leaf | Node;
struct Cur { pos: i32 }
function build(c: Cur, depth: i32): (T, Cur) {
    if (depth == 0) {
        return (Leaf { x: 1 }, Cur { pos: c.pos + 1 });
    }
    var kids: T[] = [];
    var i: i32 = 0;
    while (i < 3) {
        var (k, c2) = build(c, depth - 1);
        c = c2;
        kids = kids.append(k);
        i = i + 1;
    }
    return (Node { kids: kids }, Cur { pos: c.pos + 1 });
}
function count(t: T): i32 {
    match (t) {
        Leaf(l) => { return l.x; },
        Node(n) => {
            var s: i32 = 0;
            var i: i32 = 0;
            while (i < n.kids.len()) {
                s = s + count(n.kids[i]);
                i = i + 1;
            }
            return s;
        }
    }
    return 0;
}
function main(): i32 {
    var total: i32 = 0;
    var r: i32 = 0;
    while (r < 20) {
        var (t, c2) = build(Cur { pos: 0 }, 5);
        total = total + count(t);
        r = r + 1;
    }
    return (total / 20) - 243 + __rc_underflow_count();
}`,
	},
	{
		// ARRAY-BEARING cursor struct (Par-shaped — the self-host parser's)
		// threaded through `(value, cursor)` tuple returns. The param is
		// consumed-promoted (it is reassigned) but borrow-taint keeps it
		// out of freeEligible; the entry inc used to be freeEligible-gated
		// too, so the reassignment's unconditional overwrite dec stole the
		// caller's count on every call, and the caller's destructure-temp
		// deep drop freed a cursor box that live bindings still referenced
		// (the self-host modload fixpoint crashed in __fern_alloc this way).
		name: "tuple_return_arraybearing_cursor_threading",
		// #4587 turned out NOT to be a wasm rc bug: the underflow count is 0 on
		// every backend (post-#4582). The case's `total` computes to a
		// deterministic 9180 (= 306 * 30) identically on x86-64, arm64, AND wasm,
		// so the value guard `(total / 30) - 306` is 0 everywhere. The earlier
		// `- 50` left the value part at 256; x86-64 / arm64 read main's result as
		// an 8-bit EXIT CODE, where 256 & 255 == 0 masked the miscalibration,
		// while wasm's runWasm reads the full printed i32 and surfaced the 256 —
		// which read as "256 wasm underflows" but was the value term, not an rc
		// over-release. Recalibrated to `- 306` so the value guard is a true 0 on
		// all three backends (and no longer aliases to 0 under the exit-byte wrap).
		src: `
struct Tok { k: i32 }
struct Par { toks: Tok[], pos: i32 }
function (p: Par) advance(): Par {
    return Par { toks: p.toks, pos: p.pos + 1 };
}
function parse_one(p: Par): (i32, Par) {
    p = p.advance();
    if (p.pos % 3 == 0) { return (2, p); }
    p = p.advance();
    return (1, p);
}
function parse_many(p: Par): (i32, Par) {
    var n: i32 = 0;
    var guard: i32 = 0;
    while (guard < 200) {
        var (s, p2) = parse_one(p);
        p = p2;
        n = n + s;
        guard = guard + 1;
    }
    return (n, p);
}
function main(): i32 {
    var t0: Tok[] = [Tok { k: 1 }, Tok { k: 2 }];
    var total: i32 = 0;
    var r: i32 = 0;
    while (r < 30) {
        var (n, pf) = parse_many(Par { toks: t0, pos: 0 });
        total = total + n + pf.pos % 7;
        r = r + 1;
    }
    return (total / 30) - 306 + __rc_underflow_count();
}`,
	},
	{
		// ARRAY param threaded by reassignment — the array sibling of the
		// cursor-struct case above, and the #6021 latent double-free.
		// computeConsumedParams promoted a reassigned struct / tuple / enum
		// param but not an ARRAY one, while the Assign lowering emits the
		// overwrite dec for every rc-tracked slot alike. So `acc = ce(n, acc)`
		// on a borrowed `string[]` param released a reference the caller never
		// handed over: `none`'s count went one short, the caller's own drop
		// then freed a buffer its slot still held, and the exit sweep dec'd a
		// freed block. Silent on main (the doubly-freed block was recycled
		// harmlessly) until a heap-layout shift made the poisoned freelist head
		// get popped — a ~50% segfault in the self-host driver, from
		// irlower.precise_drop_names threading `none` through
		// astwalk.collect_idents_stmt, whose StmtIf arm is exactly this shape.
		//
		// The match matters: one arm reassigns the param and one returns
		// straight through, so the count is stolen on the first call and spent
		// on the second. `work` wraps the loop so its exit sweep — where the
		// over-release lands — runs BEFORE main reads the counter.
		name: "array_param_threaded_by_reassignment",
		src: `
enum S { Leaf(i32), Node(i32) }
function walk_expr(n: i32, acc: string[]): string[] { return acc.append("z"); }
function walk_stmt(st: S, acc: string[]): string[] {
    match (st) {
        Leaf(n) => { return walk_expr(n, acc); },
        Node(n) => {
            acc = walk_expr(n, acc);
            return acc;
        }
    }
    return acc;
}
function work(body: S[]): i32 {
    var none: string[] = [];
    var k: i32 = 0;
    var t: i32 = 0;
    while (k < body.len()) {
        var r: string[] = walk_stmt(body[k], none);
        t = t + r.len();
        k = k + 1;
    }
    return t + none.len();
}
function main(): i32 {
    var d: i32 = work([Node(1), Leaf(2)]);
    return (d - 2) + __rc_underflow_count();
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
			if c.skipWasm != "" {
				t.Skip(c.skipWasm)
			}
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("%s: got exit %d, want 0 (wrong value or rc over-release)", c.name, got)
			}
		})
	}
}
