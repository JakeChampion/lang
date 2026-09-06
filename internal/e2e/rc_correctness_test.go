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
		// #7544: the last-use test in computeArraySetIncs is TEXTUAL, so
		// inside a loop the textually-last `.with` occurrence re-executes and
		// its in-place store is observed by the NEXT iteration's read of the
		// same array. Distinct from the alias collision below: there is no
		// alias here at all, just a read earlier in the same body.
		// Before the loop gate: interpreter 2, x86-64 and arm64 10.
		//
		// The accumulator shape (`a = a.with(i, v)`) must stay in place or
		// threading goes quadratic (#4838); it takes computeArraySetIncs'
		// reassign-to-self early return and never reaches the gate. Measured:
		// the append-cliff figures are identical across the fix.
		name: "array_with_inplace_loop_read_before_with",
		src: `
function main(): i32 {
    var xs: i32[] = [1, 2];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 2) {
        acc = acc + xs[0];
        var ys: i32[] = xs.with(0, 9);
        i = i + 1;
    }
    return (acc - 2) + __rc_underflow_count();
}`,
	},
	{
		// The per-ITERATION half of the same collision, which pins the
		// arraySetConsumedReinit side of the guard: the declaration, the
		// alias and the consuming `.with` all sit inside the loop body, so
		// each iteration re-runs them. The straight-line cases below exercise
		// arraySetConsumed only, and would pass with half the guard removed.
		// Before the fix: interpreter 30, x86-64 54.
		name: "array_with_inplace_receiver_alias_in_loop",
		src: `
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var xs: i32[] = [1, 2, 3];
        var y: i32[] = xs;
        var zs: i32[] = xs.with(0, 9);
        acc = acc + y[0] + zs[0];
        i = i + 1;
    }
    return (acc - 30) + __rc_underflow_count();
}`,
	},
	{
		// The same collision on the simplest possible array: no rc-tracked
		// element, so nothing is freed and nothing over-releases — the
		// in-place `.with` is just VISIBLE through the alias. i32[] pins the
		// value-semantics half on its own, since the string[] case below
		// could otherwise pass for an rc-accounting bug.
		// Before the fix: interpreter 19, x86-64 / arm64 / wasm 99.
		name: "array_with_inplace_receiver_alias_scalar_elems",
		src: `
function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    var y: i32[] = xs;
    var zs: i32[] = xs.with(0, 9);
    var c: i32 = y[0];
    var d: i32 = zs[0];
    return (c - 1) + (d - 9) + __rc_underflow_count();
}`,
	},
	{
		// `.with` on a bare-ident receiver at its LAST use takes
		// __fern_arr_cow_inplace's rc == 1 branch and mutates the buffer in
		// place. An alias of that receiver (`var y = xs`) is a borrow
		// candidate for the #4402 dead-alias cancellation, and cancelling its
		// transfer inc is what leaves the buffer at rc 1 — so the in-place
		// mutation becomes visible through the alias and array value semantics
		// break. Both reads are pinned: y[0] is still the 11-char original
		// ("aaaaaaaaaa" + "!") and zs[0] is the 3-char replacement.
		// Before the arraySetConsumed guard, x86-64 and arm64 both read 3 for
		// y[0] while the interpreter read 11.
		// The two reads must be BOUND, not folded into the return
		// expression: inlining them changes the use order enough that the
		// borrow is not taken and the case goes vacuous (measured — the
		// inlined form returns 0 on a compiler without the guard).
		name: "array_with_inplace_receiver_alias_keeps_inc",
		src: `
function mkstr(p: string): string { return p + "!"; }
function main(): i32 {
    var xs: string[] = [mkstr("aaaaaaaaaa"), mkstr("b")];
    var y: string[] = xs;
    var zs: string[] = xs.with(0, mkstr("zz"));
    var c: i32 = y[0].len();
    var d: i32 = zs[0].len();
    return (c - 11) + (d - 3) + __rc_underflow_count();
}`,
	},
	{
		// For-in element borrow (#6888): a read-only loop element takes no
		// retain and no per-iteration deep drop — the container's buffer
		// keeps it alive. This is the happy path: values must be exact and
		// the underflow counter must stay 0, which catches the borrow
		// wrongly widening into a release (the element or the container
		// dec'd while borrowed). The loop lives in a HELPER so its exit
		// sweep — where every suppressed dec of this change would sit — has
		// run before main reads the counter; a main-resident loop reads the
		// counter inside its own return expression, before its sweep.
		// 11+1 + 1+1 = 14.
		name: "forin_elem_borrow_reads_only",
		src: `
struct S { name: string, fields: string[] }
function mkstr(p: string): string { return p + "!"; }
function scan(): i32 {
    var xs: S[] = [S{ name: mkstr("aaaaaaaaaa"), fields: [mkstr("f")] },
                   S{ name: mkstr(""), fields: [mkstr("")] }];
    var n: i32 = 0;
    for sd in xs { n = n + sd.name.len() + sd.fields.len(); }
    return n;
}
function main(): i32 {
    var c: i32 = scan();
    return (c - 14) + __rc_underflow_count();
}`,
	},
	{
		// A mid-loop early return whose value never names the element is the
		// one shape where the borrow is TAKEN and a function exit runs mid-
		// iteration: the sweep at that return releases the iterand while the
		// element stays unswept (borrowed), which is the ordering the design
		// leans on ("exit sweeps run at returns, after any read on that
		// path"). 11+1 = 12 crosses the threshold on the first element.
		name: "forin_elem_borrow_early_return",
		src: `
struct S { name: string, fields: string[] }
function mkstr(p: string): string { return p + "!"; }
function scan_early(xs: S[]): i32 {
    var n: i32 = 0;
    for sd in xs {
        n = n + sd.name.len() + sd.fields.len();
        if (n > 11) { return n; }
    }
    return n;
}
function main(): i32 {
    var c: i32 = scan_early([S{ name: mkstr("aaaaaaaaaa"), fields: [mkstr("f")] },
                             S{ name: mkstr("b"), fields: [] }]);
    return (c - 12) + __rc_underflow_count();
}`,
	},
	{
		// The return-escape shape where the returned guard is LOAD-BEARING
		// at runtime: the iterand is an owned CALL RESULT, so the sweep at
		// the in-loop return deep-frees the container inside the callee —
		// there is no caller-side may-alias conservatism to absorb an
		// uncounted return (unlike the param-container case below, where a
		// wrong borrow degrades to a leak). @noinline keeps the call
		// boundary real — inlined, the return becomes an assignment that
		// takes its own transfer inc and the case goes vacuous.
		// Verified non-vacuous: with forinElemReturnsConfined and
		// bindingConfinedToArm knocked out of walk 3 this fails under
		// free-on on x86-64, arm64 AND wasm (recycled churn read), and
		// passes with the guards restored.
		name: "forin_elem_escape_return_owned_container",
		src: `
struct S { name: string, fields: string[] }
function mkstr(p: string): string { return p + "!"; }
function mks(): S[] { return [S{ name: mkstr("aaaaaaaaaa"), fields: [mkstr("f")] }]; }
@noinline function pick_owned(): S {
    for sd in mks() {
        if (sd.fields.len() == 1) { return sd; }
    }
    return S{ name: mkstr("z"), fields: [] };
}
function use_owned(): i32 {
    var hit: S = pick_owned();
    var churn: S[] = [S{ name: mkstr("zzzzzzzzzz"), fields: [mkstr("g")] }];
    var ok: i32 = 0;
    if (hit.name == "aaaaaaaaaa!") { ok = 1; }
    return ok + churn.len() - 1;
}
function main(): i32 {
    return (use_owned() - 1) + __rc_underflow_count();
}`,
	},
	{
		// A loop element RETURNED out of the iteration: the returned guard
		// (with bindingConfinedToArm and movedLocals behind it) refuses the
		// borrow, so sd keeps the owned model. Pinned here as cross-backend
		// value correctness — content compare after the container dies, with
		// same-size churn after the drop. Measured with every walk-3 guard
		// knocked out: this still exits 0 on all three backends, because a
		// wrongly-borrowed return goes uncounted through move-on-return and
		// the caller's may-alias-result conservatism degrades its container
		// drops to FLAT decs — a leak, which this corpus reads as 0 by
		// design. The failing-mode net for these guards is the IR layer:
		// internal/ir/forin_elem_borrow_test.go, whose returned case DOES
		// fail with the guard removed.
		name: "forin_elem_escape_return_keeps_retain",
		src: `
struct S { name: string, fields: string[] }
function mkstr(p: string): string { return p + "!"; }
function pick(xs: S[]): S {
    for sd in xs {
        if (sd.fields.len() == 1) { return sd; }
    }
    return S{ name: mkstr("z"), fields: [] };
}
function use_returned(): i32 {
    var src: S[] = [S{ name: mkstr("aaaaaaaaaa"), fields: [mkstr("f")] }];
    var hit: S = pick(src);
    src = [];
    var churn: S[] = [S{ name: mkstr("zzzzzzzzzz"), fields: [mkstr("g")] }];
    var ok: i32 = 0;
    if (hit.name == "aaaaaaaaaa!") { ok = 1; }
    return ok + churn.len() - 1;
}
function main(): i32 {
    var c: i32 = use_returned();
    return (c - 1) + __rc_underflow_count();
}`,
	},
	{
		// A projection of the element RETURNED mid-loop (#8178): `return
		// sd.name` keeps the element borrowed — the string leaves with the
		// Return's own transfer inc — and this is the runtime shape where
		// that inc is load-bearing: the iterand is an owned CALL RESULT, so
		// the sweep at the in-loop return deep-frees the container inside
		// the callee, elements and all, while the caller reads the field it
		// was handed. @noinline keeps the call boundary real. Content compare
		// after same-size churn recycles the freelist; the underflow counter
		// catches the field being released under the caller.
		name: "forin_elem_borrow_return_string_field",
		src: `
struct S { name: string, fields: string[] }
function mkstr(p: string): string { return p + "!"; }
function mks(): S[] { return [S{ name: mkstr("aaaaaaaaaa"), fields: [mkstr("f")] },
                              S{ name: mkstr("b"), fields: [] }]; }
@noinline function pick_name(): string {
    for sd in mks() {
        if (sd.fields.len() == 1) { return sd.name; }
    }
    return mkstr("z");
}
function use_name(): i32 {
    var hit: string = pick_name();
    var churn: S[] = [S{ name: mkstr("zzzzzzzzzz"), fields: [mkstr("g")] },
                      S{ name: mkstr("y"), fields: [] }];
    var ok: i32 = 0;
    if (hit == "aaaaaaaaaa!") { ok = 1; }
    return ok + churn.len() - 2;
}
function main(): i32 {
    return (use_name() - 1) + __rc_underflow_count();
}`,
	},
	{
		// The ARRAY-FIELD twin: `return sd.fields` hands back a buffer the
		// freed container's element also owned. The transfer inc is what
		// keeps it at rc 1 for the caller once the element's deep drop
		// releases its own unit; the element read and length after churn
		// prove the buffer and its string survived.
		name: "forin_elem_borrow_return_array_field",
		src: `
struct S { name: string, fields: string[] }
function mkstr(p: string): string { return p + "!"; }
function mks(): S[] { return [S{ name: mkstr("aaaaaaaaaa"), fields: [mkstr("ffffffffff")] },
                              S{ name: mkstr("b"), fields: [] }]; }
@noinline function pick_fields(): string[] {
    for sd in mks() {
        if (sd.fields.len() == 1) { return sd.fields; }
    }
    return [];
}
function use_fields(): i32 {
    var hit: string[] = pick_fields();
    var churn: S[] = [S{ name: mkstr("zzzzzzzzzz"), fields: [mkstr("gggggggggg")] },
                      S{ name: mkstr("y"), fields: [] }];
    var ok: i32 = 0;
    if (hit.len() == 1 && hit[0] == "ffffffffff!") { ok = 1; }
    return ok + churn.len() - 2;
}
function main(): i32 {
    return (use_fields() - 1) + __rc_underflow_count();
}`,
	},
	{
		// A SCALAR read through the element returned mid-loop: nothing
		// pointer-shaped leaves, so the borrow holds with no retain at all,
		// and the sweep at that return releases the owned container under
		// the borrowed element after the read. 1 + 1 (a `-1` miss) = 1.
		name: "forin_elem_borrow_return_scalar",
		src: `
struct S { name: string, fields: string[] }
function mkstr(p: string): string { return p + "!"; }
function mks(): S[] { return [S{ name: mkstr("aaaaaaaaaa"), fields: [mkstr("f")] },
                              S{ name: mkstr("b"), fields: [] }]; }
@noinline function count_fields(k: i32): i32 {
    for sd in mks() {
        if (sd.name.len() == k) { return sd.fields.len(); }
    }
    return 0 - 1;
}
function main(): i32 {
    var c: i32 = count_fields(11) + count_fields(2) + count_fields(99);
    return (c - 0) + __rc_underflow_count();
}`,
	},
	{
		// A loop element STORED into an accumulator array: movedLocals and
		// bindingConfinedToArm (append is a retain sink per
		// calleeRetainsAnyArg) refuse the borrow. Same story as the return
		// case above: pinned as cross-backend value correctness with
		// in-function container death and churn; the escape site takes its
		// own transfer inc, so even a wrongly-taken borrow shows up as a
		// leak-shaped imbalance, not an exit-code failure — the IR tests
		// carry the guard-removal net.
		name: "forin_elem_escape_append_keeps_retain",
		src: `
struct S { name: string, fields: string[] }
function mkstr(p: string): string { return p + "!"; }
function collect(): i32 {
    var xs: S[] = [S{ name: mkstr("aaaaaaaaaa"), fields: [mkstr("f")] },
                   S{ name: mkstr("b"), fields: [] }];
    var acc: S[] = [];
    for sd in xs {
        if (sd.fields.len() == 1) { acc = acc.append(sd); }
    }
    xs = [];
    var churn: S[] = [S{ name: mkstr("zzzzzzzzzz"), fields: [mkstr("y")] },
                      S{ name: mkstr("xxxxxxxxxx"), fields: [mkstr("w")] }];
    var ok: i32 = 0;
    if (acc[0].name == "aaaaaaaaaa!") { ok = 1; }
    return ok + churn.len() - 2;
}
function main(): i32 {
    var c: i32 = collect();
    return (c - 1) + __rc_underflow_count();
}`,
	},
	{
		// Iterating while the LOOP'S OWN iterand is consumed by an in-place
		// `.with`: the synthetic iter local's retain is the only thing
		// holding the buffer at rc 2, forcing __fern_arr_cow_inplace to
		// copy. The loop must keep reading the ORIGINAL elements (snapshot
		// semantics) while the rebound xs sees the replacement. If the
		// element borrow ever cancelled the ITERAND's retain as well, the
		// mutation would become visible mid-loop and the overwritten
		// element would be deep-dropped under the borrow.
		// 11 + 1 = 12 from the snapshot, 3 from the replacement.
		name: "forin_iterand_with_inplace_snapshot",
		src: `
function mkstr(p: string): string { return p + "!"; }
function snap(): i32 {
    var xs: string[] = [mkstr("aaaaaaaaaa"), mkstr("")];
    var n: i32 = 0;
    for s in xs {
        n = n + s.len();
        xs = xs.with(0, mkstr("zz"));
    }
    return (n - 12) + (xs[0].len() - 3);
}
function main(): i32 {
    var c: i32 = snap();
    return c + __rc_underflow_count();
}`,
	},
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
		// An f-string reassigned in a loop. The classifiers that decide
		// whether a destination local may release its superseded value read
		// the raw expression, and an f-string reached each one's
		// conservative default, so the store dropped the old buffer on the
		// floor: 32 bytes a round on x86-64 and wasm, and arm64 clean, which
		// is why only a three-backend gate catches it (#8697).
		//
		// `f"{i}"` alone never leaked — it desugars to a bare to_string()
		// with no concat — so the literal tail is load-bearing.
		name: "fstring_reassign_releases_superseded",
		src: `
import "core/int";
import "std/i32";
function main(): i32 {
    var s: string = "";
    var i: i32 = 0;
    while (i < 50) { s = f"{i}-iteration"; i = i + 1; }
    return (s.len() - 12) + __rc_underflow_count();
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
    var f = (d: i32): i32 => { return xs[2] + d; };
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
    var f = (x: i32): i32 => { return x + k; };
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
        var f = (x: i32): i32 => { return base + x; };
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
        var f = (d: i32): i32 => { return xs[2] + d; };
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
    var outer = (a: i32): i32 => {
        var inner = (b: i32): i32 => { return xs[2] + a + b; };
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
    var f = (d: i32): i32 => { return s.v + d; };
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
    var f = (i: i32): i32 => { return base[i]; };
    var g = (i: i32): i32 => { return base[i] + 1; };
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
    return (): i32 => { return s.len(); };
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
		// refs + frees the box. Tainting the bindings leaks every buffer.
		// Churned 200x — a leak would grow the heap
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
		// Map with STRUCT values (valKind 4). Without retain-on-set/get and
		// a drop, struct map values leak entirely. They route through the
		// generated __drop_map_struct_<Item> loop
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
		// #8277: a Map READ with an ALIASED key stranded the map's key
		// buffer on the native single-word ABI — the escape analysis
		// treated every string argument of a `__method_` call as possibly
		// retained by the callee, which suppressed the caller's own
		// scope-exit release of the key.
		//
		// THREE conditions have to coincide, and the case above misses two
		// of them, which is why the corpus ran green through the whole bug:
		//
		//   1. the key needs a HEAP buffer — `"key"` is 3 bytes, so it is
		//      SSO-inline and there is no buffer to strand;
		//   2. it must be a RUNTIME concat — `"k" + "ey"` constant-folds to
		//      a literal, whose data-8 sentinel makes __fern_str_dec a
		//      no-op, so nothing is allocated in the first place;
		//   3. it must be an ALIAS at the read — a fresh concat passed
		//      straight to `m.get(...)` is reclaimed by the argument path.
		//
		// So: a stem the compiler cannot fold, a key well past seven bytes,
		// and the same `key` local read by all three verbs. Per iter:
		// get_or 1 + has 2 + get 4 = 7; 200x → 1400. Over-release trips the
		// underflow detector; the leak is what the leak gate weighs, where
		// this case is absent from every baseline and so must read 0.
		name: "map_aliased_key_read_free",
		src: `
import "core/map";
function mk(seed: i32): i32 {
    var stem: string = "a";
    var m: Map[string, string] = map_new(8);
    var key: string = stem + "-key-well-past-seven-bytes";
    m = m.insert(key, stem + "-value-well-past-seven-bytes");
    var n: i32 = 0;
    if (m.get_or(key, "") == "a-value-well-past-seven-bytes") { n = n + 1; }
    if (m.has(key)) { n = n + 2; }
    match (m.get(key)) { Some(v) => { if (v == "a-value-well-past-seven-bytes") { n = n + 4; } }, None => {} }
    return n;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(k); k = k + 1; }
    return (total - 1400) + __rc_underflow_count();
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
	// `m.without(k)` shapes, ONE PER CASE.
	//
	// These were a single `map_delete_tuple_churn_free` running all of them
	// in one body. That case's rc-corpus exit code covered the #4388 tuple
	// header (a box without the 8-byte rc header has its scope-exit drop
	// read allocator metadata at [data-8] as the rc — underflow on wasm, a
	// segfault on native), and it still does, in every case below.
	//
	// What one body could NOT do is attribute a leak. Its single leak-gate
	// number covered four independent bugs at once, so a fix to one of them
	// could not bank a zero and a regression in another could hide inside
	// somebody else's fix. Measuring them apart is what showed the shapes
	// differ: the two bound forms and the miss reclaim completely under the
	// #8276 seam retain + projection credit, while `m = m.without(k).0` is a
	// SELF-ASSIGNMENT whose RHS aliases the LHS and needs a different fix
	// entirely (docs/rc-log/ and #8276). Same coverage, four verdicts.
	{
		// Bound whole-tuple result, then the `m = t.0` reassign idiom.
		// Per iter: hit 2 + surviving "other" 3 = 5; 500x → 2500.
		name: "map_delete_bound_reassign_churn_free",
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
    var st = sm.without("ke" + "y");
    sm = st.0;
    if (st.1) { acc = acc + 2; }
    acc = acc + sm.get_or("ot" + "her", 0);
    return acc;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 2500) + __rc_underflow_count();
}`,
	},
	{
		// The SELF-ASSIGNMENT: the projected result is stored back into the
		// receiver's own binding, and on the in-place COW branch that is the
		// same handle the assignment is about to overwrite-dec. The distinct
		// shape of the four, and the one the #8276 retain does not fix.
		// Per iter: surviving "other" 3; 500x → 1500.
		name: "map_delete_projected_self_assign_churn_free",
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
    sm = sm.without("th" + "ird").0;
    acc = acc + sm.get_or("ot" + "her", 0);
    return acc;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 1500) + __rc_underflow_count();
}`,
	},
	{
		// The projection in a CALL-ARGUMENT position — the delete tuple is a
		// temporary in the one place that looked most like the shapes the seam
		// retain must NOT reach. It gets the retain because the field read
		// deep-drops the box; `sizeof(m.insert(..))` next door still does not,
		// because nothing drops that one. Guards the boundary from the leaking
		// side, which the ir-level count cannot (#8434).
		// Per iter: the surviving entry 1; 500x -> 500.
		name: "map_delete_projected_call_arg_churn_free",
		src: `
import "core/int";
import "core/map";
import "std/string";
function sizeof(m: Map[string, i32]): i32 { return m.len(); }
function mk(): i32 {
    var acc: i32 = 0;
    var sm: Map[string, i32] = map_new(8);
    sm = sm.insert("ke" + "y", 7);
    sm = sm.insert("ot" + "her", 3);
    acc = acc + sizeof(sm.without("ke" + "y").0);
    return acc;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 500) + __rc_underflow_count();
}`,
	},
	{
		// Delete MISS. Worth its own case because it leaks identically to a
		// hit — nothing about what the delete did to the table matters, which
		// is what ruled the deleted entry out as the cause (#8434).
		// Per iter: surviving "other" 3; 500x → 1500.
		name: "map_delete_bound_miss_churn_free",
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
    var sm2 = sm.without("zz" + "zz");
    sm = sm2.0;
    if (sm2.1) { acc = acc + 100; }
    acc = acc + sm.get_or("ot" + "her", 0);
    return acc;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 1500) + __rc_underflow_count();
}`,
	},
	{
		// DESTRUCTURING the tuple — the spelling computeMapCowBindSites'
		// `*ast.Destructure` arm was written for, and which nothing covered.
		// It is the one shape the seam retain already reaches, so it isolates
		// what that retain does and does not buy.
		// Per iter: hit 2 + surviving "other" 3 = 5; 500x → 2500.
		name: "map_delete_destructure_churn_free",
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
    var (m2, ok) = sm.without("ke" + "y");
    sm = m2;
    if (ok) { acc = acc + 2; }
    acc = acc + sm.get_or("ot" + "her", 0);
    return acc;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 2500) + __rc_underflow_count();
}`,
	},
	{
		// The i32-KEY half, hit and miss. No key column to walk, so it
		// separates what the delete shapes cost from what the string key
		// column costs — the split that showed the leak is ABI-independent.
		// Per iter: hit 2 + surviving 2-entry 6 = 8; 500x → 4000.
		name: "map_delete_i32_key_churn_free",
		src: `
import "core/int";
import "core/map";
function mk(): i32 {
    var acc: i32 = 0;
    var im: Map[i32, i32] = map_new(8);
    im = im.insert(1, 4);
    im = im.insert(2, 6);
    var it = im.without(1);
    im = it.0;
    if (it.1) { acc = acc + 2; }
    var im2 = im.without(9);
    im = im2.0;
    if (im2.1) { acc = acc + 100; }
    acc = acc + im.get_or(2, 0);
    return acc;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 500) { total = total + mk(); k = k + 1; }
    return (total - 4000) + __rc_underflow_count();
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
    var short_key: str = slice_unchecked(src, 0, 1);   // inline (1 byte, tagged)
    var short_val: str = slice_unchecked(src, 2, 4);   // inline (2 bytes, tagged)
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
    var f = (): i32 => { return s.len(); };
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
    var f: () => i32 = (): i32 => { return p.0.len() + p.1; };
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
        var f = (d: i32): i32 => { return it.tags[1] + d; };
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
        var f = (d: i32): i32 => { return items.len() + d; };
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
		// dropFnNameFor. Bailing on any EnumType with Args and flat-dec'ing
		// leaks the Option box + Item + its buffer, so it substitutes
		// (Option[Item] → Some(Item)), confirms the
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
		// dropStructField has to deep-drop plain arrays too, not only
		// array-OF-struct fields: a flat-dec'd `data: i32[]` leaks its
		// buffer. It frees the buffer via __fern_arr_dec on the
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
		// the branchless uniform path would flat-dec and leak the array
		// buffer, so this steers to variant-plan and frees it per
		// tag-guarded arm. Churned.
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
		// slot buffer + box via __fern_drop_arr_str on every backend.
		// Churned 100x so any unbalanced
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
		// The safety net for #6885's Cell[string] reclamation: `get` hands out
		// a RETAINED reference, so every borrowing consumer now releases it
		// and the cell's own drop / `set` pre-drop FREE the slot buffer rather
		// than merely decrementing. This is the case that says each of those
		// releases is guarded by the buffer's own count — the element aliases
		// a live local, the read is returned, stored into an array that
		// outlives the cell, and written back into the cell it came from, and
		// every check is on CONTENT, which a premature free turns into wrong
		// bytes rather than a wrong length. The literal / inline-SSO / empty
		// sources cover what __fern_str_dec short-circuits on instead of
		// freeing.
		name: "cell_string_read_aliased",
		src: `
import "core/int";
import "std/string";
function two(a: string, b: string): string { return a + b; }
function eat(s: string): i32 { if (s == "abcdefgh") { return 1; } return 0; }
function escapes(): string {
    var c: Cell[string] = cell_new(two("abcd", "efgh"));
    return c.get();
}
function main(): i32 {
    var ok: i32 = 0;
    var s: string = two("abcd", "efgh");
    var c: Cell[string] = cell_new(s);
    if (c.get() == "abcdefgh") { ok = ok + 1; }
    ok = ok + eat(c.get());
    if (c.get() + "!" == "abcdefgh!") { ok = ok + 1; }
    if (s == "abcdefgh") { ok = ok + 1; }
    if (escapes() == "abcdefgh") { ok = ok + 1; }
    var arr: string[] = [c.get()];
    if (arr[0] == "abcdefgh") { ok = ok + 1; }
    var back: string = c.get();
    c.set(back);
    if (c.get() == "abcdefgh") { ok = ok + 1; }
    if (back == "abcdefgh") { ok = ok + 1; }
    var d: Cell[string] = c;
    d.set(two("wxyz", "1234"));
    if (c.get() == "wxyz1234") { ok = ok + 1; }
    var lit: Cell[string] = cell_new("a-literal-past-the-inline-threshold");
    if (lit.get().len() == 35) { ok = ok + 1; }
    var sso: Cell[string] = cell_new("abc");
    if (sso.get() == "abc") { ok = ok + 1; }
    var mt: Cell[string] = cell_new("");
    if (mt.get().len() == 0) { ok = ok + 1; }
    return (12 - ok) + __rc_underflow_count();
}`,
	},
	{
		// `a[0].with(0, 9)` on a NESTED array: the receiver `a[0]` is a
		// projection out of the live outer array `a`, i.e. a BORROW — the inner
		// buffer is still owned by `a`, so an in-place cow would corrupt `a[0]`.
		// computeArraySetIncs must not treat a non-ident receiver as a dead
		// fresh temp and skip the inc: that mutates `a[0]` in place on native
		// (`a[0][0]` becomes 9) → 9 + 9 = 18 instead of the copy-on-write
		// 9 + 1 = 10. It forces the inc
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
		// The case above with ONE line changed at the caller — `a = bump(a)`
		// instead of `var b = bump(a)` — and that line was the whole gap
		// (#6057). The callee's `.with` self-reassign released the receiver a
		// second time (__fern_arr_cow_inplace decs its own source on the copy
		// branch, unlike __map_cow_inplace, whose policy the shared code path
		// applied to both), and the caller's binding is what made that release
		// land on a buffer nothing else was keeping alive: `a` reached rc -1
		// while the program still printed the right answer.
		//
		// The sibling above passes either way, because its caller keeps `a`
		// live and the #4873 grow bracket's inc/dec pair happens to absorb the
		// extra release. That is why the corpus had this construct covered and
		// still missed the bug for as long as it existed.
		name: "with_reassign_self_borrowed_param_caller_rebind",
		src: `
function bump(xs: i32[]): i32[] { xs = xs.with(0, 99); return xs; }
function main(): i32 {
    var a: i32[] = [1, 2];
    a = bump(a);
    return (a[0] - 99) + (a[1] - 2) + __rc_underflow_count();
}`,
	},
	{
		// `.with` on an array MATCH-BOUND from a borrowed enum: the arm binds
		// the payload with no retain, so the array sits at the box's rc==1 and
		// an in-place cow rewrote the payload of the enum `snap` still held,
		// then the old box's drop released the stored leaf (use-after-free).
		// The persistent-HAMT node update shape. Correct: snap keeps 39 at
		// slot 0 and t reads 100 → 39 + 100 - 139 = 0.
		name: "with_on_match_binding_of_borrowed_enum",
		src: `
enum N { L(i32), B(i32, N[]) }
function bump(n: N, idx: i32, v: i32): N {
    match (n) {
        L(x) => { return n; },
        B(c, kids) => { return B(c + 1, kids.with(idx, L(v))); },
    }
}
function leaf(n: N, idx: i32): i32 {
    match (n) {
        L(x) => { return x; },
        B(c, kids) => { match (kids[idx]) { L(x) => { return x; }, B(q, r) => { return -1; } } },
    }
}
function main(): i32 {
    var t: N = B(0, [L(1), L(2), L(3)]);
    var i: i32 = 0;
    while (i < 40) { t = bump(t, i % 3, i); i = i + 1; }
    var snap: N = t;
    t = bump(t, 0, 100);
    return (leaf(snap, 0) + leaf(t, 0) - 139) + __rc_underflow_count();
}`,
	},
	{
		// A reassigned loop counter is a scalar built by arithmetic. Under the
		// blanket Binary taint it counted as "may alias a borrow", and the
		// any-arg-tainted call rule then stranded `t` at exit — while the same
		// loop with `bump(t, 1)` reclaimed everything. The leak gate holds this
		// at 0 on both natives.
		name: "loop_counter_arg_keeps_result_reclaimable",
		src: `
enum N { L(i32), B(i32, N[]) }
function bump(n: N, v: i32): N {
    match (n) {
        L(x) => { return n; },
        B(c, kids) => { return B(c + v, kids); },
    }
}
function main(): i32 {
    var t: N = B(0, [L(1), L(2), L(3)]);
    var i: i32 = 0;
    while (i < 3) { t = bump(t, i); i = i + 1; }
    match (t) { L(x) => { return 7; }, B(c, k) => { return c - 3 + __rc_underflow_count(); } }
}`,
	},
	{
		// A Map param threaded through `m = m.insert(..)` sits on the borrow
		// baseline (typeDeepDropWired keeps Maps off the consumed promotion).
		// On the cow-copy path its overwrite dec used to release the caller's
		// handle, which the frame never owned: with `g` aliasing `base` after
		// an in-place iteration, the next copy left `base` at rc 1 with two
		// owners and g's re-declaration drop freed it (exit 121 on a plain
		// build, a use-after-free under the sanitizer). The ownership bit
		// (cowMapParams) decs only what the frame owns and releases it at
		// exit, so the shape is value-correct and reclaims fully.
		name: "map_param_cow_thread_literal_arg",
		src: `import "core/map";
function grow(m: Map[i32, i32], k: i32): Map[i32, i32] {
    m = m.insert(k, k * 7);
    return m;
}
function main(): i32 {
    var base: Map[i32, i32] = map_new(8);
    base = base.insert(1, 11);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 40) {
        var g: Map[i32, i32] = grow(base, 2);
        acc = acc + g.get_or(1, 0) + g.get_or(2, 0);
        i = i + 1;
    }
    if (base.get_or(1, 0) != 11) { return 121; }
    if (acc != 40 * 25) { return 120; }
    return __rc_underflow_count();
}`,
	},
	{
		// The same `.with` self-reassign reached through a LOCAL ALIAS of the
		// borrowed param rather than the param itself, threaded recursively —
		// the shape the self-host checker's e060_collect_dyn_locals is built
		// from, and where #6057 was first caught in the wild (52 over-releases
		// compiling parser.fern, 92 compiling checker.fern, while emitting
		// byte-identical output).
		//
		// It needs its own case because the alias is NOT a consumed-threaded
		// param, so it takes a different arm of the fix: no overwrite dec at
		// all, where the param arm decs under its ownership bit.
		name: "with_reassign_local_alias_threaded",
		src: `
function collect(depth: i32, acc: string[]): string[] {
    var a: string[] = acc;
    var i: i32 = 0;
    while (i < 3) {
        if (a.len() > 0) { a = a.with(0, "x"); }
        a = a.append("n");
        if (depth > 0) { a = collect(depth - 1, a); }
        i = i + 1;
    }
    return a;
}
function main(): i32 {
    var s: string[] = [];
    s = collect(3, s);
    if (s.len() < 1) { return 91; }
    return __rc_underflow_count();
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
        var piece: str = slice_unchecked(base, 0, 20 + (i - (i / 30) * 30));
        s = need(s, "structdroptestprefix:" + piece);
        i = i + 1;
    }
    var bad: i32 = 0;
    var j: i32 = 0;
    while (j < 40) {
        var want: i32 = 21 + 20 + (j - (j / 30) * 30);
        if (s.needed[j].len() != want) { bad = bad + 1; }
        if (slice_unchecked(s.needed[j], 0, 20) != "structdroptestprefix") { bad = bad + 1; }
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
        var t: Tok = Tok { kind: i, text: "tokentextprefixvalue:" + slice_unchecked(base, 0, 20 + (i - (i / 30) * 30)) };
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
		// out of freeEligible. The entry inc must NOT be freeEligible-gated
		// too, or the reassignment's unconditional overwrite dec steals the
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
	{
		// A match payload binding and a differently-TYPED local in a SIBLING
		// arm, both named `a`. Neither shadows the other — their scopes are
		// disjoint — so shadowrename left both bare, and the IR builder's flat
		// `locals[string]int32` map collapsed them onto one slot. The
		// name-keyed type lookups then answer for whichever declaration they
		// reach first, so one arm's value is released with the other's drop
		// plan.
		//
		// The self-host compiler's irlower.alias_names_in_stmt is this shape
		// verbatim (`parser.StmtAssign(a)` beside `var a: string[]` in the
		// StmtIf / StmtMatch arms), which cost one over-release per ASSIGNMENT
		// STATEMENT in every program it compiled — the largest single
		// contributor to the driver's over-release count after #6021.
		name: "sibling_scope_name_collision_binding_vs_local",
		src: `
struct Asg { k: i32 }
struct Vr { k: i32 }
enum St { SVar(Vr), SAssign(Asg), SIf(i32) }
function vals(x: i32, acc: string[]): string[] { return acc.append("z"); }
function walk(st: St, acc: string[]): string[] {
    match (st) {
        SVar(v) => { return vals(v.k, acc); },
        SAssign(a) => { return vals(a.k, acc); },
        SIf(n) => {
            var a: string[] = acc;
            a = vals(n, a);
            return a;
        }
    }
    return acc;
}
function work(body: St[]): i32 {
    var acc: string[] = [];
    var i: i32 = 0;
    while (i < body.len()) { acc = walk(body[i], acc); i = i + 1; }
    return acc.len();
}
function main(): i32 {
    var n: i32 = work([SVar(Vr{k:1}), SAssign(Asg{k:2}), SIf(3)]);
    return (n - 3) + __rc_underflow_count();
}`,
	},
	{
		// A tuple destructure whose names SHADOW an enclosing local. The
		// checker registers each destructure name as a synthetic *ast.Var
		// that lives only in info.Locals, so renaming the Destructure node's
		// []string left the slot registered under the old name: the compiler
		// died outright with `ir: destructure name "a$1" has no slot
		// (compiler bug)`. A hard compiler crash, on main, for a program the
		// interpreter runs fine — reached here because the sibling-scope
		// rename above makes the pass fire on far more programs.
		name: "shadowed_tuple_destructure_keeps_its_slot",
		src: `
function f(): i32 {
    var a: i32 = 1;
    {
        var (a, b) = (20, 3);
        return a + b;
    }
}
function main(): i32 { return f() - 23 + __rc_underflow_count(); }`,
	},
	{
		// The same class one binding form later: a destructuring PARAMETER
		// pattern inside a LAMBDA body, whose names shadow an enclosing
		// local. The checker registers a lambda body's locals against the
		// lambda's synthetic FuncDecl, not the enclosing function, so the
		// rename found no synthetic Var to follow and the compiler died with
		// the same `has no slot` refusal as the case above. Reached once the
		// pass started walking lambda bodies at all (#7151); before that a
		// shadowed name inside a lambda was never renamed, which was a wrong
		// ANSWER rather than a refusal.
		name: "shadowed_param_pattern_in_lambda_keeps_its_slot",
		src: `
struct Point { x: i32, y: i32 }
function main(): i32 {
    var x: i32 = 1;
    var f = (Point { x, y }: Point): i32 => { return x * 10 + y; };
    var g = ((x, y): (i32, i32)): i32 => { return x + y; };
    return f(Point { x: 3, y: 4 }) + g((2, 3)) - 39 + x - 1 + __rc_underflow_count();
}`,
	},
	{
		// A closure SHARED between a struct field and a live local (#6443).
		// The field release dispatches through the drop-fn pointer the pair
		// carries, so it has to respect the pair's own count: at rc>1 the
		// is_unique gate must decline and leave the env alone, or `h` calls a
		// freed env and reads a recycled capture. `keep` holds the struct so
		// both references are live across the calls.
		name: "closure_struct_field_shared_with_local",
		src: `
import "core/int";
import "std/i32";
struct H { f: (i32) => i32 }
function main(): i32 {
    var n: i32 = 7;
    var h: (i32) => i32 = (x: i32) => x + n;
    var keep: H = H { f: h };
    var a: i32 = h(1);
    var b: i32 = (keep.f)(2);
    var c: i32 = h(3);
    return (a + b + c - 27) + __rc_underflow_count();
}`,
	},
	{
		// The safety boundary of the closure-call arg reclaim (#6460). Each
		// closure here HANDS BACK its argument — as the result, inside a
		// struct, inside an array — so the caller's fresh temp is still live
		// after the call and must NOT be released. The gate is the result
		// TYPE: only a concrete scalar admits the reclaim, and every callee
		// below returns something pointer-shaped, so all of them decline.
		//
		// The reads after each call are the point: a premature release would
		// hand the buffer to the freelist, the next construction would recycle
		// it, and the sum would come out wrong even where the underflow
		// counter stayed quiet.
		name: "closure_call_arg_handed_back_is_not_reclaimed",
		src: `
import "core/int";
import "std/i32";
struct Hold { xs: i32[] }
function main(): i32 {
    var idf: (i32[]) => i32[] = (xs: i32[]) => xs;
    var wrap: (i32[]) => Hold = (xs: i32[]) => Hold { xs: xs };
    var nest: (i32[]) => i32[][] = (xs: i32[]) => [xs];
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 20) {
        var a: i32[] = idf([1, 2, 3]);
        var b: Hold = wrap([4, 5, 6]);
        var c: i32[][] = nest([7, 8, 9]);
        t = t + a[0] + a[2] + b.xs[1] + c[0][2];
        r = r + 1;
    }
    return (t - 360) + __rc_underflow_count();
}`,
	},
	{
		// A closure whose result IS a scalar — so the reclaim fires — called
		// with an argument that is a live LOCAL rather than a fresh temp. Only
		// a freshly-allocating shape is stashed and released; an aliased local
		// must be left alone, or the binding it is still read through is freed
		// underneath it.
		name: "closure_call_arg_alias_is_left_alone",
		src: `
import "core/int";
import "std/i32";
function main(): i32 {
    var len: (i32[]) => i32 = (xs: i32[]) => xs.len();
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 20) {
        var a: i32[] = [1, 2, 3];
        t = t + len(a) + len(a) + a[1];
        r = r + 1;
    }
    return (t - 160) + __rc_underflow_count();
}`,
	},
	{
		// The fresh-container field read (#6401) applied to a container the
		// caller still owns: `pick` RETURNS ITS OWN PARAMETER, so
		// `pick(bx).items` hands back the struct `bx` holds, and `bx` is read
		// again after every extraction.
		//
		// What this pins is the RUNTIME backstop, not the static predicate.
		// Measured: deliberately breaking the static gate so it claims this
		// container leaves the case passing on all three backends, because
		// `return <param>` emits the return-transfer inc and the container
		// arrives at rc 2 — emitOwnedSlotDrop's is_unique gate then declines
		// and only decs. That is the safety argument the lowering rests on,
		// so it is worth a case; it is NOT evidence that the static gate is
		// correct, and a change that removed the is_unique gate is what this
		// would catch.
		name: "field_read_off_param_returning_call_is_not_claimed",
		src: `
import "core/int";
import "std/i32";
struct P { a: i32, b: i32 }
struct Box { items: P[], tag: i32 }
function pick(b: Box): Box { return b; }
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 20) {
        var bx: Box = Box { items: [P { a: 1, b: 2 }, P { a: 3, b: 4 }], tag: 5 };
        var a: P[] = pick(bx).items;
        var c: P[] = pick(bx).items;
        t = t + a[0].a + c[1].b + bx.items[1].a + bx.tag;
        r = r + 1;
    }
    return (t - 260) + __rc_underflow_count();
}`,
	},
	{
		// The claiming shape, looping so the container's freed blocks are
		// recycled into the next round. Every field kind the retain has to
		// know about is extracted from a fresh container: an array, a heap
		// string past SSO, and a scalar (which takes the no-retain path).
		// An unbalanced retain leaks — invisible here — but an unbalanced
		// DROP hands a live block to the freelist, and the next round's
		// construction reads it back.
		name: "field_read_off_fresh_container_reclaims",
		src: `
import "core/int";
import "std/i32";
import "std/string";
struct P { a: i32, b: i32 }
struct Box { items: P[], tag: i32, name: string }
function mk(i: i32): Box {
    return Box { items: [P { a: i, b: i + 1 }, P { a: i + 2, b: i + 3 }], tag: i, name: "boxed-name-" + i.to_string() };
}
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 20) {
        var a: P[] = mk(r).items;
        var s: string = mk(r).name;
        var g: i32 = mk(r).tag;
        t = t + a[0].a + a[1].b + s.len() + g;
        r = r + 1;
    }
    return (t - 880) + __rc_underflow_count();
}`,
	},
	{
		// The reclaiming shape: a struct holding a capturing closure, built
		// and discarded in a loop so the freed pair / env / capture blocks are
		// recycled into the next round. An over-release here hands a live
		// block to the freelist and the next round reads it back — which the
		// value check catches even when the underflow counter does not.
		name: "closure_struct_field_reclaim_loop",
		src: `
import "core/int";
import "std/i32";
import "std/string";
struct P { name: string, f: (i32) => i32 }
function mkP(n: i32): P { return P { name: "provider" + n.to_string(), f: (x: i32) => x + n }; }
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 20) {
        var ps: P[] = [];
        var i: i32 = 0;
        while (i < 4) { ps = ps.append(mkP(i)); i = i + 1; }
        var j: i32 = 0;
        while (j < ps.len()) { t = t + (ps[j].f)(1) + ps[j].name.len(); j = j + 1; }
        r = r + 1;
    }
    return (t - 920) + __rc_underflow_count();
}`,
	},
	{
		// `.with` on a `string[]` PARAM, where the caller keeps the receiver
		// live so the CoW takes its copy branch (#6407). Until strings joined
		// the counted-element set, that copy went through the plain
		// __fern_arr_cow_inplace: a raw memcpy leaving the fresh buffer
		// sharing the caller's element pointers at unchanged rc. Both arrays
		// then walk-dropped the same strings — a double free, and a SIGSEGV
		// on x86-64 rather than the leak the issue was filed as.
		//
		// The elements are CONCATENATED rather than written as literals: a
		// string literal is a static-sentinel buffer that inc/dec
		// short-circuit on, so a literal-element array cannot express this
		// bug at all. The rounds matter for the same reason — one round
		// double-decs a buffer nobody reuses, and it takes a freed-and-
		// recycled block for the corruption to become a fault.
		name: "string_array_with_through_param_copies_elements",
		src: `
import "core/int";
import "std/i32";
import "std/string";
function swap0(a: string[]): string[] { return a.with(0, a[1]); }
function mks(n: i32): string[] {
    var out: string[] = [];
    var i: i32 = 0;
    while (i < n) { out = out.append("kkkkkkkkkk" + i.to_string()); i = i + 1; }
    return out;
}
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 20) {
        var a: string[] = mks(6);
        var b: string[] = swap0(a);
        t = t + a[2].len() + b[0].len();
        r = r + 1;
    }
    return (t - 440) + __rc_underflow_count();
}`,
	},
	{
		// The same store where the REPLACED element is SHARED with a live
		// local. `keep` holds the element that gets overwritten, so the
		// release `.with` now owes must only DEC it, never free — otherwise
		// `keep` reads a recycled buffer.
		//
		// One-directional, like the rest of this corpus: the pre-#6407
		// behaviour (no release at all) leaks, and a leak reads 0 here too.
		// It gates the release not turning into an over-release later.
		name: "string_array_with_releases_replaced_element_once",
		src: `
import "core/int";
import "std/string";
function cat(a: string, b: string): string { return a + b; }
function main(): i32 {
    var fresh: string = cat("aaaaaaaaa", "bbbbbbbb");
    var a: string[] = [fresh, "ccccccccccccccccc"];
    var keep: string = a[0];
    a = a.with(0, "ddddddddddddddddd");
    return (keep.len() + a[0].len() - 34) + __rc_underflow_count();
}`,
	},
	{
		// A consuming (`own`) walk over a list whose spine the CALLER still
		// holds (#6720). `Cons(7, shared)` is a fresh construction — so E051
		// admits it as an owned argument — but its tail is borrowed, and the
		// construction retains it, so the walk meets a cell at rc 2 one level
		// in.
		//
		// The walk decides reuse per level from that cell's own refcount, and
		// a cell reached only through a surviving box's tail field counts 1.
		// So declining at the shared cell was not enough: the level below saw
		// 1, called itself unique, and reused a cell `shared` still reaches.
		// `sum(shared)` then read the rebuilt list — 8 rather than 6, off a
		// block the allocator had already handed back out.
		//
		// The stop has to propagate down the spine, which is what the decline
		// branch's retain of the moved-out payloads buys.
		name: "own_consume_over_borrowed_tail_keeps_caller_list",
		src: `
import "core/int";
enum List { Cons(i32, List), Nil }
function build(n: i32): List {
    if (n == 0) { return Nil; }
    return Cons(n, build(n - 1));
}
function inc_all(own xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function sum(l: List): i32 {
    match (l) { Cons(h, t) => { return h + sum(t); }, Nil => { return 0; } }
}
function main(): i32 {
    var shared: List = build(3);
    var ys: List = inc_all(Cons(7, shared));
    var a: i32 = sum(ys);
    var b: i32 = sum(shared);
    return (a - 17) + (b - 6) + __rc_underflow_count();
}`,
	},
	{
		// The same shape run in a loop, so the cells the walk frees are
		// recycled before the caller reads them. The single-shot case above
		// catches the reuse of a still-reachable cell; this one catches it
		// having been handed to a later allocation, which is the form the
		// defect takes in a real traversal.
		name: "own_consume_over_borrowed_tail_recycles",
		src: `
import "core/int";
enum List { Cons(i32, List), Nil }
function build(n: i32): List {
    if (n == 0) { return Nil; }
    return Cons(n, build(n - 1));
}
function inc_all(own xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function sum(l: List): i32 {
    match (l) { Cons(h, t) => { return h + sum(t); }, Nil => { return 0; } }
}
function main(): i32 {
    var shared: List = build(4);
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 20) {
        var ys: List = inc_all(Cons(9, shared));
        t = t + sum(ys) + sum(shared);
        r = r + 1;
    }
    return (t - 680) + __rc_underflow_count();
}`,
	},
	{
		// The four shapes the loop-body push move must NOT fire on (#6533).
		// Widening its dominance guard trades a leak for a use-after-free when
		// it is wrong, and every one of these would free an element something
		// still reads: only the leak direction is recoverable, so each shape
		// gets a value check that a freed-and-recycled box would fail.
		//
		// The element is loop-carried, so it outlives the buffer's drop.
		// 4 pushes + kind 7 + the stored element's kind 7 = 18.
		name: "loop_push_outer_local_element_survives",
		src: `
import "core/int";
struct Val { kind: i32, kids: i32[] }
function build(n: i32): i32 {
    var vals: Val[] = [];
    var v: Val = Val { kind: 7, kids: [1] };
    var i: i32 = 0;
    while (i < n) {
        if (i == 9999) { continue; }
        vals = vals.append(v);
        i = i + 1;
    }
    return vals.len() + v.kind + vals[0].kind;
}
function main(): i32 { return (build(4) - 18) + __rc_underflow_count(); }`,
	},
	{
		// The element is stored into TWO buffers. Only the second push is its
		// last use, so only that one may move; the first must retain, or both
		// buffers' drops release the one reference. 4 + 4 + 1 + 1 = 10.
		name: "loop_push_element_aliased_into_two_buffers",
		src: `
import "core/int";
struct Val { kind: i32, kids: i32[] }
function build(n: i32): i32 {
    var a: Val[] = [];
    var b: Val[] = [];
    var i: i32 = 0;
    while (i < n) {
        if (i == 9999) { break; }
        var v: Val = Val { kind: i, kids: [i] };
        a = a.append(v);
        b = b.append(v);
        i = i + 1;
    }
    return a.len() + b.len() + a[0].kids.len() + b[0].kids.len();
}
function main(): i32 { return (build(4) - 10) + __rc_underflow_count(); }`,
	},
	{
		// An early exit that carries the element OUT. The push is no longer the
		// element's last use, so it must retain — a move would let the buffer's
		// drop free the value the caller is handed. kind 2 + kids[0] 2 = 4.
		name: "loop_push_early_exit_returns_the_element",
		src: `
import "core/int";
struct Val { kind: i32, kids: i32[] }
function build(n: i32): Val {
    var vals: Val[] = [];
    var i: i32 = 0;
    while (i < n) {
        var v: Val = Val { kind: i, kids: [i] };
        vals = vals.append(v);
        if (i == 2) { return v; }
        i = i + 1;
    }
    return Val { kind: -1, kids: [] };
}
function main(): i32 {
    var got: Val = build(5);
    return (got.kind + got.kids[0] - 4) + __rc_underflow_count();
}`,
	},
	{
		// An early exit BETWEEN the declaration and the push: the exiting
		// iteration builds an element it never stores, so the move is refused
		// and the element keeps its retain. 3 stored + stop 1 + kids[0] 0 = 4.
		name: "loop_push_exit_between_decl_and_push",
		src: `
import "core/int";
struct Val { kind: i32, kids: i32[] }
function build(n: i32): i32 {
    var vals: Val[] = [];
    var stop: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var v: Val = Val { kind: i, kids: [i] };
        if (i == 3) { stop = 1; break; }
        vals = vals.append(v);
        i = i + 1;
    }
    return vals.len() + stop + vals[0].kids[0];
}
function main(): i32 { return (build(5) - 4) + __rc_underflow_count(); }`,
	},
	{
		// A LIVE `continue` before the element's declaration, so the declaration
		// is genuinely skipped on half the iterations. That is the path the
		// widened guard rests on: the element's slot then still holds the
		// previous iteration's pointer, which the buffer already owns, and the
		// suppressed drop must leave it alone. i = 1, 3, 5 are stored, so
		// (1+1) + (2+3) + (3+5) = 15.
		name: "loop_push_live_continue_before_decl",
		src: `
import "core/int";
struct Val { kind: i32, kids: i32[] }
function build(n: i32): i32 {
    var vals: Val[] = [];
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        i = i + 1;
        if (i % 2 == 0) { continue; }
        var v: Val = Val { kind: i, kids: [i] };
        vals = vals.append(v);
        total = total + vals.len() + vals[vals.len() - 1].kids[0];
    }
    return total;
}
function main(): i32 { return (build(6) - 15) + __rc_underflow_count(); }`,
	},
	{
		// A LIVE `break` before the declaration: the loop leaves with the slot
		// holding the last stored element, which the buffer owns. 3 stored,
		// kinds 0 + 1 + 2 = 3, so 3 + 3 = 6.
		name: "loop_push_live_break_before_decl",
		src: `
import "core/int";
struct Val { kind: i32, kids: i32[] }
function build(n: i32): i32 {
    var vals: Val[] = [];
    var i: i32 = 0;
    while (i < n) {
        if (i == 3) { break; }
        var v: Val = Val { kind: i, kids: [i] };
        vals = vals.append(v);
        i = i + 1;
    }
    var sum: i32 = 0;
    var k: i32 = 0;
    while (k < vals.len()) { sum = sum + vals[k].kids[0]; k = k + 1; }
    return vals.len() + sum;
}
function main(): i32 { return (build(9) - 6) + __rc_underflow_count(); }`,
	},
	{
		// A map value read out and back many times, with the map outliving
		// every read (#6561). `m.get(k)` hands back a reference the MAP
		// still owns, so the box reclaim added for the lookup leak must not
		// reach the payload: a string value read 200 times and read once
		// more afterwards is 200 * 28 + 28.
		name: "map_get_string_value_borrowed_repeatedly",
		src: `
import "core/map";
import "std/string";
function main(): i32 {
    var index: Map[string, string] = map_new(64);
    index = index.insert("k", "a-heap-string-value-past-sso");
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        match (index.get("k")) { Some(s) => { acc = acc + s.len(); }, None => { return 998; } }
        i = i + 1;
    }
    match (index.get("k")) { Some(s) => { acc = acc + s.len(); }, None => { return 997; } }
    return (acc - 5628) + __rc_underflow_count();
}`,
	},
	{
		// An accumulator SEEDED from a container read, with the source array
		// read again afterwards (#6567). The seed is a counted alias of the
		// element, so reclaiming the accumulator's intermediates must leave
		// every element intact: 200 * 25 for the lines, 19 for the elements.
		name: "accumulator_seeded_from_array_element",
		src: `
function build(words: string[]): string {
    var line: string = words[0];
    var g: i32 = 1;
    while (g < words.len()) { line = line + "  " + words[g]; g = g + 1; }
    return line;
}
function main(): i32 {
    var words: string[] = ["alpha", "beta", "gamma", "delta"];
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) { acc = acc + build(words).len(); i = i + 1; }
    var j: i32 = 0;
    while (j < words.len()) { acc = acc + words[j].len(); j = j + 1; }
    return (acc - 5019) + __rc_underflow_count();
}`,
	},
	// #6877 — a loop-body `var` whose LAST use is a consuming `.with`
	// receiver. __fern_arr_cow_inplace takes that reference over, so the
	// next iteration's re-declaration must not release the slot as well;
	// it did, and the freed buffer was the one the RESULT still pointed at.
	// The four shapes below all crashed or silently double-freed on both
	// natives; the three after them were already correct and are the
	// controls that catch a re-narrowing.
	{
		name: "with_loop_local_from_fresh_container_field",
		src: `
struct P { a: i32, b: i32 }
struct Box { items: P[], tag: i32 }
function mk_box(): Box { return Box { items: [P{a:0,b:0}, P{a:1,b:1}], tag: 0 }; }
function f(n: i32): i32 {
    var t: i32 = 0;
    for i in 0..n {
        var it: P[] = mk_box().items;
        var a: P[] = it.with(0, P{a:i,b:i});
        t = t + a[0].a;
    }
    return t;
}
function main(): i32 { return (f(20) - 190) + __rc_underflow_count(); }`,
	},
	{
		// The container read is not the trigger: a call returning the array
		// directly breaks identically (it aborted with "array index out of
		// range" rather than SIGSEGV, which is the same corruption read
		// through a recycled length word).
		name: "with_loop_local_from_call_result",
		src: `
struct P { a: i32, b: i32 }
function mk_arr(): P[] { return [P{a:0,b:0}, P{a:1,b:1}]; }
function f(n: i32): i32 {
    var t: i32 = 0;
    for i in 0..n {
        var it: P[] = mk_arr();
        var a: P[] = it.with(0, P{a:i,b:i});
        t = t + a[0].a;
    }
    return t;
}
function main(): i32 { return (f(20) - 190) + __rc_underflow_count(); }`,
	},
	{
		// Binding the container first, so the field read is a plain
		// projection out of a live local rather than a fresh-temp read.
		name: "with_loop_local_from_bound_container_field",
		src: `
struct P { a: i32, b: i32 }
struct Box { items: P[], tag: i32 }
function mk_box(): Box { return Box { items: [P{a:0,b:0}, P{a:1,b:1}], tag: 0 }; }
function f(n: i32): i32 {
    var t: i32 = 0;
    for i in 0..n {
        var bx: Box = mk_box();
        var it: P[] = bx.items;
        var a: P[] = it.with(0, P{a:i,b:i});
        t = t + a[0].a;
    }
    return t;
}
function main(): i32 { return (f(20) - 190) + __rc_underflow_count(); }`,
	},
	{
		// A SCALAR element array double-freed too, without ever computing a
		// wrong answer — FERN_LEAKCHECK reported allocs=20 frees=39 and
		// live_bytes=-304 while the program exited 0. The struct-element
		// shapes above are the same bug with something left to corrupt, so
		// this row is what stops the fix being narrowed to pointer elements.
		name: "with_loop_local_scalar_elements",
		src: `
function mk_ints(): i32[] { return [0, 1]; }
function f(n: i32): i32 {
    var t: i32 = 0;
    for i in 0..n {
        var it: i32[] = mk_ints();
        var a: i32[] = it.with(0, i);
        t = t + a[0];
    }
    return t;
}
function main(): i32 { return (f(20) - 190) + __rc_underflow_count(); }`,
	},
	{
		// Control: a read of the receiver AFTER the `.with` makes the call a
		// borrow, so the receiver keeps its own reference and BOTH releases
		// are owed. The re-init drop must still fire here.
		name: "with_loop_local_read_after_set",
		src: `
struct P { a: i32, b: i32 }
function mk_arr(): P[] { return [P{a:0,b:0}, P{a:1,b:1}]; }
function f(n: i32): i32 {
    var t: i32 = 0;
    for i in 0..n {
        var it: P[] = mk_arr();
        var a: P[] = it.with(0, P{a:i,b:i});
        t = t + a[0].a + it[1].b;
    }
    return t;
}
function main(): i32 { return (f(20) - 210) + __rc_underflow_count(); }`,
	},
	{
		// Control: `.append` is not a consuming receiver, so nothing is
		// transferred and the re-init drop is the only release.
		name: "append_loop_local_from_call_result",
		src: `
struct P { a: i32, b: i32 }
function mk_arr(): P[] { return [P{a:0,b:0}, P{a:1,b:1}]; }
function f(n: i32): i32 {
    var t: i32 = 0;
    for i in 0..n {
        var it: P[] = mk_arr();
        var a: P[] = it.append(P{a:i,b:i});
        t = t + a[2].a;
    }
    return t;
}
function main(): i32 { return (f(20) - 190) + __rc_underflow_count(); }`,
	},
	{
		// Control: the `.with` receiver is a fresh temp with no binding at
		// all, so there is no slot to re-initialise.
		name: "with_loop_no_intermediate_local",
		src: `
struct P { a: i32, b: i32 }
function mk_arr(): P[] { return [P{a:0,b:0}, P{a:1,b:1}]; }
function f(n: i32): i32 {
    var t: i32 = 0;
    for i in 0..n {
        var a: P[] = mk_arr().with(0, P{a:i,b:i});
        t = t + a[0].a;
    }
    return t;
}
function main(): i32 { return (f(20) - 190) + __rc_underflow_count(); }`,
	},
	{
		// `.with` straight off a fresh container read. The read owns what it
		// produced, so cow_inplace takes that reference; the borrow-shaped
		// siblings — a field of a live struct, an element of a live array —
		// must still copy, or the container is mutated through the receiver.
		// This row is the guard on the borrow half — a classification that
		// widens changes the answer here; the leak the fresh half used to
		// pay is invisible to an exit code and is gated by
		// TestX86_64AllocScaling instead.
		name: "with_receiver_fresh_container_read_vs_borrowed",
		src: `
struct S { xs: i32[], tag: i32 }
function mk(): S { return S { xs: [1, 2], tag: 0 }; }
function borrowed_field(s: S): i32 {
    var b: i32[] = s.xs.with(0, 99);
    return b[0] + s.xs[0];
}
function borrowed_elem(a: i32[][]): i32 {
    var b: i32[] = a[0].with(0, 99);
    return b[0] + a[0][0];
}
function fresh_field(): i32 {
    var b: i32[] = mk().xs.with(0, 99);
    return b[0] + b[1];
}
function main(): i32 {
    var s: S = mk();
    var aa: i32[][] = [[1, 2], [3, 4]];
    var t: i32 = 0;
    t = t + borrowed_field(s) - 100;
    t = t + borrowed_elem(aa) - 100;
    t = t + fresh_field() - 101;
    t = t + s.xs[0] + aa[0][0] - 2;
    return t + __rc_underflow_count();
}`,
	},
	// #6417 — the fresh-scrutinee box release sits at the post-match JOIN, so
	// an arm that leaves the match early branches straight past it. These
	// pin the release being emitted at each such exit as well; the leak they
	// cost is measured by TestX86_64AllocScaling, and what the corpus adds is
	// that the extra release is not an OVER-release.
	{
		name: "match_boxed_result_returning_arms",
		src: `
function make(i: i64): Result[i64, i64] {
    if (i % 2i64 == 0i64) { return Ok(i); }
    return Err(i);
}
function pick(i: i64): i32 {
    match (make(i)) { Ok(v) => { return (v as i32) + 1; }, Err(_) => { return 0; } }
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { t = t + pick(i as i64); i = i + 1; }
    return (t - 10000) + __rc_underflow_count();
}`,
	},
	{
		name: "match_boxed_result_continue_arm",
		src: `
function make(i: i64): Result[i64, i64] {
    if (i % 2i64 == 0i64) { return Ok(i); }
    return Err(i);
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        match (make(i as i64)) { Ok(v) => { t = t + (v as i32) + 1; }, Err(_) => { i = i + 1; continue; } }
        i = i + 1;
    }
    return (t - 10000) + __rc_underflow_count();
}`,
	},
	{
		// Expression form: an arm body is an expression, but a value block
		// can still `return`.
		name: "match_expr_boxed_result_returning_arm",
		src: `
function make(i: i64): Result[i64, i64] {
    if (i % 2i64 == 0i64) { return Ok(i); }
    return Err(i);
}
function pick(i: i64): i32 {
    var r: i32 = match (make(i)) { Ok(v) => { return (v as i32) + 1; }, Err(_) => 0 };
    return r;
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { t = t + pick(i as i64); i = i + 1; }
    return (t - 10000) + __rc_underflow_count();
}`,
	},
	{
		// The `m.get(k)` rebox reaches the same join. Its free must stay
		// SHALLOW wherever it is emitted — the payload belongs to the map.
		name: "match_map_get_returning_arms",
		src: `
import "core/map";
function pick(m: Map[string, string], k: string): i32 {
    match (m.get(k)) { Some(v) => { return v.len(); }, None => { return 0; } }
}
function main(): i32 {
    var m: Map[string, string] = map_new(8);
    m = m.insert("a", "xy" + "zw");
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { t = t + pick(m, "a"); i = i + 1; }
    return (t - 800) + m.get_or("a", "").len() - 4 + __rc_underflow_count();
}`,
	},
	{
		// An `@` binding names the box itself and the arm hands it straight
		// back, so the release emitted at that return must net against the
		// return-transfer inc rather than freeing what the caller receives.
		name: "match_at_binding_returned_from_arm",
		src: `
function make(i: i64): Result[i64, i64] {
    if (i % 2i64 == 0i64) { return Ok(i); }
    return Err(i);
}
function pick(i: i64): Result[i64, i64] {
    match (make(i)) { whole @ Ok(v) => { return whole; }, Err(_) => { return Err(0i64); } }
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        match (pick(i as i64)) { Ok(v) => { t = t + (v as i32) + 1; }, Err(_) => { } }
        i = i + 1;
    }
    return (t - 10000) + __rc_underflow_count();
}`,
	},
	{
		// string[][] whose inner arrays and their elements are aliased by
		// live locals, dropped in a loop. The outer drop reclaims each inner
		// string[] through __fern_drop_arr_str, so the element strings reach
		// __fern_str_dec once per outer array — an over-release here would
		// hit the shared local. 200 rounds * (4 + 4) for the reads, plus
		// 4 for the local that outlives them.
		name: "nested_string_array_element_aliased",
		src: `
import "core/int";
import "std/string";
function cat(a: string, b: string): string { return a + b; }
function main(): i32 {
    var s: string = cat("ab", "cd");
    var inner: string[] = [s, s];
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var outer: string[][] = [inner, [s]];
        acc = acc + outer[0][1].len() + outer[1][0].len();
        i = i + 1;
    }
    return (acc + s.len() - 1604) + __rc_underflow_count();
}`,
	},
	{
		// A struct literal consuming an owned string local at its last use
		// moves the string into the field (no field-init inc, no exit-sweep
		// dec). `keep` is the guard on that: its string is read AFTER the
		// literal, so the move must not fire and both readings must still
		// see live characters — a wrong move frees the buffer under the
		// second read. 200 * (16 + 13) for the moved pair, 2 * 14 for keep.
		name: "struct_string_field_move_and_alias",
		src: `
import "core/int";
import "std/i32";
import "std/string";
struct W { name: string, n: i32 }
struct Acc { last: string, total: i32 }
function mk(k: i32): W {
    var s: string = "payload-string-" + k.to_string();
    return W { name: s, n: k };
}
function step(a: Acc, k: i32): Acc {
    var s: string = "acc-payload-" + k.to_string();
    return Acc { last: s, total: a.total + k };
}
function keep(k: i32): i32 {
    var s: string = "kept-payload-" + k.to_string();
    var w: W = W { name: s, n: k };
    return w.name.len() + s.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var a: Acc = Acc { last: "", total: 0 };
    var i: i32 = 0;
    while (i < 200) {
        var w: W = mk(i % 8);
        a = step(a, i % 8);
        acc = acc + w.name.len() + a.last.len();
        i = i + 1;
    }
    return (acc + keep(3) - 5828) + __rc_underflow_count();
}`,
	},
	{
		// A source-declared receiver method with an identity return, whose
		// result is consumed by another call. The caller reclaims that
		// result under the is_unique gate, so the identity path — where the
		// result aliases a receiver the caller still owns — is what the gate
		// has to hold: `base` and `b` are both read after the aliasing call.
		// 200 * (18 + 20 + 21 + 21) for the string half, 200 * (15 + 15) for
		// the struct half.
		name: "method_identity_return_result_reclaimed",
		src: `
import "core/int";
import "std/i32";
import "std/string";
struct Box { tag: string, n: i32 }
function (s: string) tail(n: i32): str {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return slice_unchecked(s, n, sLen);
}
function (b: Box) relabel(t: string): Box {
    if (t.len() == 0) { return b; }
    return Box { tag: t, n: b.n };
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var base: string = "long-enough-payload-" + (i % 8).to_string();
        acc = acc + base.tail(3).len() + base.tail(1).to_owned().len();
        acc = acc + base.tail(0).len() + base.len();
        var b: Box = Box { tag: "start-tag-value", n: i % 8 };
        acc = acc + b.relabel("").tag.len() + b.tag.len();
        i = i + 1;
    }
    return (acc - 22000) + __rc_underflow_count();
}`,
	},
	{
		// A struct element of an array bound to a local, whose string fields
		// ALIAS a live local (#6499). The exit sweep's inline struct-box
		// reclaim frees each string field with __fern_str_dec, so this is the
		// case that says the free is guarded by the field's own count: `s`
		// and the array element are read for their CONTENT after both the
		// bound copy and the argument temp have been swept, which a premature
		// free turns into wrong bytes rather than a wrong length.
		name: "struct_array_element_binding_aliased",
		src: `
import "core/int";
import "std/string";
struct E { key: string, value: string }
function cat(a: string, b: string): string { return a + b; }
function take(e: E): i32 { return e.key.len(); }
function main(): i32 {
    var s: string = cat("abcd", "efgh");
    var es: E[] = [E { key: s, value: s }];
    var a: E = es[0];
    var n: i32 = a.key.len() + a.value.len() + take(es[0]);
    var ok: i32 = 0;
    if (s == "abcdefgh") { ok = ok + 1; }
    if (es[0].value == "abcdefgh") { ok = ok + 1; }
    return (n - 24) + (2 - ok) + __rc_underflow_count();
}`,
	},
	{
		// The tuple sibling of the entry above (#6879): the exit sweep's
		// inline tuple arm frees a native single-word string element with
		// __fern_str_dec, so this is the case that says the free is guarded
		// by the element's own count rather than by the tuple box's. Every
		// spelling that can outlive the sweep is here — the element aliases
		// a live local, is bound out, is passed to a callee, is returned,
		// fills both slots of one tuple, and escapes into an array — and
		// each is checked for CONTENT, which a premature free turns into
		// wrong bytes rather than a wrong length. Literal / inline-SSO /
		// empty-sentinel elements cover the three sources __fern_str_dec
		// short-circuits on instead of freeing.
		name: "tuple_string_element_aliased",
		src: `
import "core/int";
import "std/string";
function two(a: string, b: string): string { return a + b; }
function take(s: string): i32 { if (s == "abcdefgh") { return 1; } return 0; }
function escapes(): string {
    var t: (string, i32) = (two("abcd", "efgh"), 1);
    return t.0;
}
function main(): i32 {
    var ok: i32 = 0;
    var s: string = two("abcd", "efgh");
    var t: (string, i32) = (s, 1);
    var u: string = t.0;
    if (t.0 == "abcdefgh") { ok = ok + 1; }
    if (s == "abcdefgh") { ok = ok + 1; }
    if (u == "abcdefgh") { ok = ok + 1; }
    ok = ok + take(t.0);
    if (escapes() == "abcdefgh") { ok = ok + 1; }
    var d: (string, string) = (s, s);
    if (d.0 == "abcdefgh") { ok = ok + 1; }
    if (d.1 == "abcdefgh") { ok = ok + 1; }
    var arr: (string, i32)[] = [t];
    if (arr[0].0 == "abcdefgh") { ok = ok + 1; }
    if (t.0 == "abcdefgh") { ok = ok + 1; }
    var lit: (string, i32) = ("a-literal-past-the-inline-threshold", 2);
    if (lit.0.len() == 35) { ok = ok + 1; }
    var sso: (string, i32) = ("abc", 3);
    if (sso.0 == "abc") { ok = ok + 1; }
    var mt: (string, i32) = ("", 4);
    if (mt.0.len() == 0) { ok = ok + 1; }
    return (12 - ok) + __rc_underflow_count();
}`,
	},
	{
		// A fresh array passed to a constructor that STORES it (#6522), plus
		// the aliased spelling of the same call. The per-argument stage-(b)
		// reclaim decs the caller's temp right after the call, so this is the
		// case that says the dec nets against the constructor's store rather
		// than freeing what the struct now owns: the stored array is read
		// back through the returned struct, and a LIVE local passed at the
		// same position is read after the call too — the alias half, where
		// the temp is not fresh and the caller must keep its reference.
		name: "fresh_array_into_constructor_stored",
		src: `
import "core/int";
struct Node { name: string, deps: i32[], mtime: i32 }
function two(a: i32, b: i32): i32[] { return [a, b]; }
function node(name: string, deps: i32[], mtime: i32): Node {
    return Node { name: name, deps: deps, mtime: mtime };
}
function main(): i32 {
    var fresh: Node = node("a", two(1, 2), 7);
    var live: i32[] = two(3, 4);
    var aliased: Node = node("b", live, 8);
    var t: i32 = fresh.deps[0] + fresh.deps[1] + aliased.deps[0] + aliased.deps[1];
    var u: i32 = live[0] + live[1] + fresh.deps.len() + aliased.deps.len();
    return (t - 10) + (u - 11) + __rc_underflow_count();
}`,
	},
	{
		// An `.append` result consumed by a borrowing call (#6501), across
		// both of __fern_arr_push_grow's outcomes — reclaiming one the way
		// the other wants is a use-after-free.
		//
		// `full` has no spare capacity, so the grow COPIES and the post-call
		// dec frees a buffer nobody else holds. `inplace`'s receiver is a
		// BORROWED param at rc==1 with room, appearing once, so the grow
		// mutates in place, sets the receiver's rc to 2 and hands the
		// receiver back — the same dec has to net that bump away and free
		// nothing, and `roomy` is read again after the call to say so.
		// `xs[0]` is the element-receiver spelling, which is not an Ident and
		// so could never reach the old forced-copy classifier at all.
		name: "append_result_consumed_by_call",
		src: `
import "core/int";
function sink(xs: i32[]): i32 { var s: i32 = 0; var i: i32 = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; }
function inplace(path: i32[]): i32 { return sink(path.append(99)); }
function main(): i32 {
    var full: i32[] = [1, 2];
    var roomy: i32[] = [];
    roomy = roomy.append(3);
    var xs: i32[][] = [[6], [7]];
    var a: i32 = sink(full.append(10)) + inplace(roomy);
    var b: i32 = sink(full) + sink(roomy) + sink(xs[0].append(30)) + sink(xs[1]);
    var c: i32 = inplace(roomy) + sink(xs[0]) + full.len() + roomy.len() + xs[0].len();
    return (a - 115) + (b - 49) + (c - 112) + __rc_underflow_count();
}`,
	},
	{
		// `dyn Trait` coerced from a struct LOCAL that stays live past the
		// coercion. The retain the alias site owes belongs on the concrete
		// `data` word: a plain __fern_rc_inc lands on the dyn representation
		// (the natives' headerless {data,vtable} cell, wasm's static vtable
		// word) instead. The top-level pair is the discriminator — the source
		// is at its last use there, so the coercion is a MOVE and no retain is
		// emitted at all — while the loop pair takes the retain and needs it:
		// `s` and the aliasing `d` are both reclaimed at scope exit.
		// 9 flat + 3 × (9 + 3) in the loop.
		name: "dyn_coerced_from_live_struct_local",
		src: `
import "core/int";
trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function main(): i32 {
    var s0: Square = Square { side: 3 };
    var d0: dyn Shape = s0;
    var flat: i32 = d0.area();
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var s: Square = Square { side: 3 };
        var d: dyn Shape = s;
        acc = acc + d.area() + s.side;
        i = i + 1;
    }
    return (flat - 9) + (acc - 36) + __rc_underflow_count();
}`,
	},
	{
		// A vtable-dispatched method's parameters — the receiver AND a second
		// owned-by-default struct handed to it. `OpCallDyn` jumps through a
		// function pointer, so there is no callee name to hang a caller-side
		// retain on; the impl method must therefore borrow both. The
		// owned-model half of this lives in
		// rc_dyn_vtable_param_test.go, which runs the same shape with borrow
		// inference off. 4 × (9 + 15 + 5).
		name: "dyn_vtable_dispatched_params_borrow",
		src: `
import "core/int";
trait Shape {
    function area(self: Self): i32;
    function scaled(self: Self, by: Factor): i32;
}
struct Factor { k: i32 }
struct Square { side: i32 }
impl Shape for Square {
    function area(self: Self): i32 { return self.side * self.side; }
    function scaled(self: Self, by: Factor): i32 { return self.side * by.k; }
}
function main(): i32 {
    var f: Factor = Factor { k: 5 };
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var d: dyn Shape = Square { side: 3 };
        acc = acc + d.area() + d.scaled(f) + f.k;
        i = i + 1;
    }
    return (acc - 116) + __rc_underflow_count();
}`,
	},
	{
		// A pair-form payload passed to a BORROWING call in the arm body
		// (#6409). `x.len()` is `__method_Array_len(x)` after the checker's
		// method rewrite, and `total(x)` is the user-function spelling of the
		// same borrow; both now excuse the occurrence, so the payload is
		// released when the arm ends.
		//
		// `ident` is the control: it returns its parameter, so the payload
		// escapes the arm and must keep its prior leak. `alias` is read AFTER
		// the match on every round — if the release admitted a pointer result
		// the read would be a use-after-free rather than a wrong value, so
		// this is the row that would fail loudly. 4 × (3 + 6) + 4 × (2 + 3).
		name: "pair_form_payload_borrowing_call",
		src: `
import "core/int";
function mk(n: i32): Option[i32[]] {
    if (n == 0) { return None; }
    return Some([n, n + 1, n + 2]);
}
function total(a: i32[]): i32 { var s: i32 = 0; var i: i32 = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }
function ident(a: i32[]): i32[] { return a; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        match (mk(1)) { Some(x) => { t = t + x.len() + total(x); }, None => { }, }
        i = i + 1;
    }
    var alias: i32[] = [];
    i = 0;
    while (i < 4) {
        match (mk(2)) { Some(x) => { alias = ident(x); }, None => { }, }
        t = t + alias[0] + alias.len();
        i = i + 1;
    }
    return (t - 56) + __rc_underflow_count();
}`,
	},
	{
		// A pair-form enum handed straight to a call, never bound, is now
		// released after the call (#6393) — a class that had no release at
		// all before, so the controls are what carry this row.
		//
		// `wrap` returns its own parameter's array, so the box is not proven
		// fresh and keeps its prior leak; `live` is read after the loop, and
		// a release admitted there would have freed it. `keep` moves the
		// payload into a container that outlives the call and `unwrap` hands
		// it straight back — both read afterwards for the same reason.
		// 8 + 40 + 56 + 10 + 24 + 32 + 5.
		name: "pair_form_enum_temp_as_argument",
		src: `
import "core/int";
struct S2 { a: i32, b: i32 }
function mk(n: i32): Option[i32[]] {
    if (n == 0) { return None; }
    return Some([n, n + 1, n + 2]);
}
function wrap(xs: i32[], n: i32): Option[i32[]] {
    if (n == 0) { return None; }
    return Some(xs);
}
function mks(n: i32): Option[S2] {
    if (n == 0) { return None; }
    return Some(S2 { a: n, b: n + 1 });
}
function fac(o: Option[i32[]]): i32 {
    match (o) { Some(a) => { return a[0]; }, None => { return 0; }, }
}
function facs(o: Option[S2]): i32 {
    match (o) { Some(s) => { return s.a + s.b; }, None => { return 0; }, }
}
function keep(o: Option[i32[]], out: i32[][]): i32[][] {
    match (o) { Some(a) => { return out.append(a); }, None => { return out; }, }
}
function unwrap(o: Option[i32[]]): i32[] {
    match (o) { Some(a) => { return a; }, None => { return [0]; }, }
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 8) { t = t + fac(mk(1)) + facs(mks(2)); i = i + 1; }
    var live: i32[] = [7, 8, 9];
    i = 0;
    while (i < 8) { t = t + fac(wrap(live, 1)); i = i + 1; }
    t = t + live[0] + live.len();
    var out: i32[][] = [];
    i = 0;
    while (i < 8) { out = keep(mk(3), out); i = i + 1; }
    i = 0;
    while (i < out.len()) { t = t + out[i][0]; i = i + 1; }
    var got: i32[] = [];
    i = 0;
    while (i < 8) { got = unwrap(mk(4)); t = t + got[0]; i = i + 1; }
    t = t + got[1];
    return (t - 175) + __rc_underflow_count();
}`,
	},
	{
		// A read out of a fresh container, consumed as a call argument or
		// chained into another read, is now reclaimed (#6401). The reclaim
		// retains what it hands on and deep-drops what it came out of, so an
		// over-claim here frees a live container rather than leaking one.
		//
		// `passthru` returns its own parameter, so `live` is what the reads
		// in the control loops project out of — nothing there may be freed,
		// and `live` is read again afterwards to say so.
		// 8 + 8 + 8 + 8 + 20 + 8 + 36 + 23.
		name: "fresh_container_read_chained",
		src: `
import "core/int";
import "std/string";
struct P { a: i32, b: i32 }
struct Box { items: P[], tag: i32 }
struct SBox { names: string[], tag: i32 }
struct Inner { vals: i32[] }
struct Outer { inner: Inner, tag: i32 }
function mk_box(k: i32): Box {
    return Box { items: [P { a: k, b: k + 1 }, P { a: k + 2, b: k + 3 }], tag: k };
}
function mk_sbox(k: i32): SBox { return SBox { names: ["nn", "mm"], tag: k }; }
function mk_outer(k: i32): Outer {
    return Outer { inner: Inner { vals: [k, k + 1, k + 2] }, tag: k };
}
function passthru(b: Box): Box { return b; }
function sink(ps: P[]): i32 { return ps.len(); }
function sinks(ns: string[]): i32 { return ns.len(); }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        t = t + sink(mk_box(1).items) + sinks(mk_sbox(1).names);
        t = t + mk_box(2).items[0].a + mk_sbox(3).names[1].len();
        t = t + mk_outer(4).inner.vals[1];
        i = i + 1;
    }
    var live: Box = mk_box(9);
    i = 0;
    while (i < 4) {
        t = t + sink(passthru(live).items) + passthru(live).items[0].a;
        i = i + 1;
    }
    t = t + live.items[1].b + live.items.len() + live.tag;
    return (t - 119) + __rc_underflow_count();
}`,
	},
	{
		// A container literal built from a bare-ident source that STAYS LIVE
		// afterwards: the construction retains the buffer, so the box and the
		// local are co-owners and each release must land exactly once. The
		// discarded-statement form runs the box's drop first, which is what
		// separates the two releases; the bound form and the escaping form
		// exercise the other two orders. 4 rounds × (2i+1) summed = 16.
		name: "construction_source_stays_live",
		src: `
struct Holder { a: i32[], b: i32[] }
function escaping(i: i32): (i32[], i32[]) {
    var xs: i32[] = [i, i + 1];
    var t: (i32[], i32[]) = (xs, [i + 2, i + 3]);
    var guard: i32 = xs[1];
    if (guard < 0) { return (xs, xs); }
    return t;
}
function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    (xs, [i + 2, i + 3]);
    Holder { a: xs, b: [i + 2, i + 3] };
    var bound: Holder = Holder { a: xs, b: [i + 2, i + 3] };
    var esc: (i32[], i32[]) = escaping(i);
    return xs[0] + xs[1] + bound.a[0] - bound.a[0] + esc.0[0] - esc.0[0];
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) { acc = acc + round(i); i = i + 1; }
    return (acc - 16) + __rc_underflow_count();
}`,
	},
	{
		// #7798: a heap string local handed to a parameter the callee never
		// reads was never freed on the native single-word string ABI.
		// computeFreeEligible taints a string ident passed to a user
		// function unless paramCountedRetain clears it — "a leak at worst,
		// never a use-after-free" — and the three counted-retain summaries
		// required at least one occurrence of the parameter, so a parameter
		// the body never mentions read as unknown and the taint stayed. It
		// is the strongest form of the property, not the absence of it: a
		// parameter nothing mentions cannot be retained by any means.
		//
		// Every row of the issue's bisection is here, including the shapes
		// that were already clean, because the fix has to leave those alone:
		// a read parameter, a temp rather than a local, an array local, an
		// alias, and a store into a container. Before: 13 allocs / 7 frees /
		// 192 bytes on x86-64, clean on arm64. After: 13 / 13 / 0 on both.
		name: "unused_param_string_never_freed",
		src: `
function two(a: string, b: string): string { return a + b; }
function ignore(s: string): i32 { return 7; }
function ignore2(s: string, t: string): i32 { return 7; }
function halfUse(a: string, b: string): i32 { return a.len(); }
function ignoreRet(s: string): string { return "z"; }
function eat(s: string): i32 { return s.len(); }
function ignoreArr(a: i32[]): i32 { return 7; }
function main(): i32 {
    var n: i32 = 0;
    var s1: string = two("abcd", "efgh");
    n = n + ignore(s1);
    var s2: string = two("abcd", "efgh");
    var s3: string = two("ijkl", "mnop");
    n = n + ignore2(s2, s3);
    var s4: string = two("abcd", "efgh");
    var s5: string = two("ijkl", "mnop");
    n = n + halfUse(s4, s5);
    var s6: string = two("abcd", "efgh");
    var r: string = ignoreRet(s6);
    n = n + r.len();
    var s7: string = two("abcd", "efgh");
    n = n + ignore(s7) + ignore(s7);
    var s8: string = two("abcd", "efgh");
    n = n + eat(s8);
    n = n + ignore(two("abcd", "efgh"));
    var a1: i32[] = [1, 2, 3];
    n = n + ignoreArr(a1);
    var s9: string = two("abcd", "efgh");
    var t9: string = s9;
    n = n + t9.len();
    var s10: string = two("abcd", "efgh");
    var arr: string[] = [s10];
    n = n + arr.len();
    if (n < 0) { return 1; }
    return __rc_underflow_count();
}
`,
	},
	{
		// #7867 slice 1. `structParamProjectionsSafe` credited
		// `__method_Array_push`'s RECEIVER and never its ELEMENT, so a
		// fresh temp handed to a callee that pushes it was stranded: the
		// callee inc'd the element into the buffer and nothing gave the
		// caller's own reference back. 2 blocks a round here (the Op box
		// and its heap string), linear and unbounded — 12 allocs / 6
		// frees at three rounds before, 12 / 12 after.
		//
		// @noinline on both is load-bearing. `inline.go` inlines a
		// single-reference callee at a loop call site, and an inlined
		// callee has no argument temp to reclaim, so the shape measures
		// clean without it whether or not the bug is present.
		//
		// The string is built by concatenation rather than written as a
		// literal: a literal is a static .rodata cell with an immortal
		// header, so the element's second block would not exist and the
		// case would understate itself.
		name: "push_elem_param_counted_pointer_field",
		src: `
struct Op { a: i32, s: string, b: i32 }
struct St { ops: Op[] }

@noinline
function mkop(i: i32, pad: string): Op { return Op { a: i, s: pad + "0123456789abcdef", b: i * 2 }; }

@noinline
function emit(s: St, o: Op): St { return St { ops: s.ops.append(o) }; }

function main(): i32 {
    var pad: string = "xyzw";
    var st: St = St { ops: [] };
    var i: i32 = 0;
    while (i < 3) {
        st = emit(st, mkop(i, pad));
        i = i + 1;
    }
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < st.ops.len()) {
        t = t * 10 + st.ops[k].a + (st.ops[k].s.len() - 20);
        k = k + 1;
    }
    return (t - 12) + __rc_underflow_count();
}
`,
	},
	{
		// The interlock the element credit must not disturb. A
		// scalar-only element type is isOwnedByDefaultType, so the
		// CALLEE reclaims the argument at exit and the caller must not —
		// `ownedByCalleeAt` suppresses the stage-(b) dec at that
		// position. This shape was already clean (9 allocs / 9 frees)
		// and stays clean; it is what would double-free first if the
		// credit ever reached an owned-by-default position.
		name: "push_elem_param_scalar_only_owned_by_default",
		src: `
struct Op { a: i32, b: i32 }
struct St { ops: Op[] }

@noinline
function mkop(i: i32): Op { return Op { a: i, b: i * 2 }; }

@noinline
function emit(s: St, o: Op): St { return St { ops: s.ops.append(o) }; }

function main(): i32 {
    var st: St = St { ops: [] };
    var i: i32 = 0;
    while (i < 3) {
        st = emit(st, mkop(i));
        i = i + 1;
    }
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < st.ops.len()) {
        t = t * 10 + st.ops[k].a;
        k = k + 1;
    }
    return (t - 12) + __rc_underflow_count();
}
`,
	},
	{
		// The identity-return shape the reclaim gate exists to refuse,
		// pinned on the ANSWER and the underflow counter rather than on
		// a byte count. That distinction is the point: an over-releasing
		// build reads BETTER on live_bytes, so only the value check and
		// __rc_underflow dissent.
		//
		// `keepf(o) -> o` is admitted today and is correct: the
		// return-transfer inc puts the temp at rc 2, and the caller's
		// is_unique-gated drop nets it to exactly one owner.
		name: "call_arg_temp_returned_identity",
		src: `
struct Op { a: i32, s: string }

@noinline
function mkop(i: i32): Op { return Op { a: i, s: "keep" }; }

@noinline
function keepf(o: Op): Op { return o; }

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var r: Op = keepf(mkop(i));
        t = t + r.a + r.s.len();
        i = i + 1;
    }
    return (t - 22) + __rc_underflow_count();
}
`,
	},
	{
		// The wrapping shape: the result REACHES the argument through
		// memory. Admitted by the struct-literal slot arm, because
		// construction inc's the aliased field, so the temp is at rc 2
		// across the caller's drop.
		//
		// The self-host's counted tier had to REFUSE this exact shape
		// (#7856) — its construction-side retain is conditional on the
		// field type routing a reclaim, and `Box { o: o }` does not.
		// Native's needsRcIncOnAlias is unconditional for every pointer
		// type, which is why the credit is sound here and was not there.
		// Pinned so that difference cannot be quietly erased.
		name: "call_arg_temp_wrapped_in_result",
		src: `
struct Op { a: i32, s: string }
struct Box { o: Op, n: i32 }

@noinline
function mkop(i: i32): Op { return Op { a: i, s: "wrap" }; }

@noinline
function wrapf(o: Op, k: i32): Box { return Box { o: o, n: k }; }

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var r: Box = wrapf(mkop(i), i * 2);
        t = t + r.o.a + r.n + r.o.s.len();
        i = i + 1;
    }
    return (t - 34) + __rc_underflow_count();
}
`,
	},
	{
		// #7867 slice 3. `inferParamCountedRetain`'s type switch had arms
		// for StringType, ArrayType and StructType and none for
		// EnumType, so an enum parameter was never credited, and a fresh
		// enum temp handed to a callee that stores it was stranded.
		//
		// This is the shape the parser is built from — `e_binary(op, l,
		// r)` and its siblings take `ast.Expr` payloads, and `ast.Expr`
		// is an enum. Before: 8 allocs / 4 frees at four rounds.
		//
		// Sound for the same reason the struct arm is: needsRcIncOnAlias
		// is true for an enum, so the struct construction inc's the
		// argument, and a parameter is never a move site.
		name: "enum_param_in_construction_slot",
		src: `
enum Ty { I32(i32), Str(string), Unk(string) }
struct Node { ty: Ty, n: i32 }

@noinline
function t_i32(): Ty { return Ty.I32(32); }

@noinline
function mknode(t: Ty, n: i32): Node { return Node { ty: t, n: n }; }

function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var nd: Node = mknode(t_i32(), i);
        acc = acc + nd.n;
        i = i + 1;
    }
    return (acc - 6) + __rc_underflow_count();
}
`,
	},
	{
		// The same for a TUPLE parameter, the other arm the switch was
		// missing. A tuple has no nominal declaration, so only the slot
		// / argument / return rules can credit it — the projection arms
		// never fire. Before: 8 allocs / 4 frees, 128 bytes.
		name: "tuple_param_in_construction_slot",
		src: `
struct Node { ty: (i32, string), n: i32 }

@noinline
function t_i32(): (i32, string) { return (32, "x"); }

@noinline
function mknode(t: (i32, string), n: i32): Node { return Node { ty: t, n: n }; }

function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var nd: Node = mknode(t_i32(), i);
        acc = acc + nd.n;
        i = i + 1;
    }
    return (acc - 6) + __rc_underflow_count();
}
`,
	},
	{
		// The control that isolated the two above, kept because it is
		// what makes them a diagnosis rather than an observation: the
		// identical call shape with a STRUCT parameter was already clean
		// (8 allocs / 8 frees), so the only thing that differed was
		// which arm of the type switch ran.
		name: "struct_param_in_construction_slot_control",
		src: `
struct Ty { w: i32, s: string }
struct Node { ty: Ty, n: i32 }

@noinline
function t_i32(): Ty { return Ty { w: 32, s: "x" }; }

@noinline
function mknode(t: Ty, n: i32): Node { return Node { ty: t, n: n }; }

function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var nd: Node = mknode(t_i32(), i);
        acc = acc + nd.n;
        i = i + 1;
    }
    return (acc - 6) + __rc_underflow_count();
}
`,
	},
	{
		// An enum parameter the callee only MATCHES on was already clean
		// and must stay so: a match reads the payload out and retains
		// nothing, so there is no counted store for the credit to rest
		// on and the caller's is_unique-gated drop is the only owner.
		name: "enum_param_matched_only",
		src: `
enum Ty { I32(i32), Str(string), Unk(string) }

@noinline
function t_str(): Ty { return Ty.Str("abcdefghij"); }

@noinline
function width(t: Ty): i32 {
    match (t) {
        Ty.I32(w) => { return w; },
        Ty.Str(s) => { return s.len(); },
        Ty.Unk(r) => { return 0; }
    }
}

function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        acc = acc + width(t_str());
        i = i + 1;
    }
    return (acc - 40) + __rc_underflow_count();
}
`,
	},
	{
		// #7876, the source half. The escape walk taints every
		// string-typed IDENT passed as a call argument, because a user
		// callee may store it into a container it returns and freeing
		// it caller-side would then dangle. Its only exemption reads
		// `paramCountedRetain`, which is keyed by USER declaration — so
		// a BUILTIN has no entry, and `slice_unchecked(line, a, b)`
		// cost `line` its scope-exit drop outright.
		//
		// It is protecting against nothing: `slice_unchecked` copies
		// bytes OUT of its source (`emitStrSliceRuntime`: "returns a
		// fresh string"), which is the fact `pureReadReceiverBuiltin`
		// already states and three other sites in rc_analysis.go
		// already rely on.
		//
		// Before: 1 alloc / 0 frees at four rounds — `line` itself,
		// leaked. After: 1 / 1. The slice length here is 4, below the
		// 7-byte inline threshold, so the slices allocate nothing and
		// the single block is unambiguously the source.
		name: "string_source_of_slice_builtin_keeps_its_drop",
		src: `
@noinline
function eat(s: str): i32 { return s.len(); }

function main(): i32 {
    var pad: string = "wxyz";
    var line: string = pad + "0123456789abcdef0123456789";
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        t = t + eat(slice_unchecked(line, 4, 8));
        i = i + 1;
    }
    return (t - 16) + __rc_underflow_count();
}
`,
	},
	{
		// #7876, the temp half. `slice_unchecked`'s result is a FRESH
		// string — `emitStrSliceRuntime` says so, and `rcResultOwned`
		// records that a release is right for all three of its shapes
		// — but `freshOwnedRcTempType` had no arm for the builtin
		// spelling, so the argument-position temp was stranded on
		// every call.
		//
		// The slice here is 16 bytes, above the 7-byte inline
		// threshold, so it is a heap copy on native. Before: 5 allocs
		// / 1 free at four rounds. The two-word ABIs have no inline
		// packing and leaked one per call at ANY length, which is why
		// the sibling case above uses a 4-byte slice and this one does
		// not — between them they cover both.
		name: "slice_builtin_arg_temp_is_reclaimed",
		src: `
@noinline
function eat(s: str): i32 { return s.len(); }

function main(): i32 {
    var pad: string = "wxyz";
    var line: string = pad + "0123456789abcdef0123456789";
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        t = t + eat(slice_unchecked(line, 4, 20));
        i = i + 1;
    }
    return (t - 64) + __rc_underflow_count();
}
`,
	},
	{
		// #7867 slice 2, the argument-temp half. A fresh concat handed
		// to a callee that only SCANS it — a copying builtin — is the
		// caller's to reclaim: the builtin memcpys or reads the bytes
		// and retains nothing, so with the parameter uncredited the
		// temp was stranded, one buffer per call (measured 202/102,
		// 3200 B live at 100 rounds; exactly 2x at 200). The callee
		// carries the state's string field into its result so the
		// call-level returnsNoParamEscape gate stays closed and the
		// per-argument credit is the only route — the EmitState.write
		// shape.
		name: "copying_builtin_arg_temp_is_reclaimed",
		src: `
struct St { tag: string, n: i32 }

@noinline
function mk(pad: string, i: i32): string { return pad + "0123456789abcdef"; }

@noinline
function scan(s: St, text: string): St {
    return St { tag: s.tag, n: s.n + __count_byte(text, 97) };
}

function main(): i32 {
    var pad: string = "xyzw";
    var st: St = St { tag: pad + "tagtagtagtag", n: 0 };
    var i: i32 = 0;
    while (i < 4) {
        st = scan(st, mk(pad, i));
        i = i + 1;
    }
    return (st.n - 4) + __rc_underflow_count();
}
`,
	},
	{
		// #7867 slice 2, the bound-local half — a distinct defect with
		// the same cause: computeFreeEligible tainted any local passed
		// to a builtin it did not know, so the binding lost its
		// FREEING scope-exit drop (measured 100/0, 3200 B live; the
		// two-word ABIs never took this taint, so the leak was
		// single-word x86-64's).
		name: "copying_builtin_bound_local_keeps_its_drop",
		src: `
@noinline
function mk(pad: string, i: i32): string { return pad + "0123456789abcdef"; }

function main(): i32 {
    var pad: string = "xyzw";
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var msg: string = mk(pad, i);
        t = t + __memchr(msg, 97, 0);
        i = i + 1;
    }
    return (t - 56) + __rc_underflow_count();
}
`,
	},
	{
		// The literal motivating builtin. strbuf_append memcpys the
		// bytes past the buffer tail (its own runtime doc, both native
		// implementations) and returns void, so the fresh concat
		// argument nets to zero owners after the caller's dec
		// (measured 101/1 before, 101/101 after).
		name: "strbuf_append_arg_temp_is_reclaimed",
		src: `
struct St { n: i32 }

@noinline
function mk(pad: string, i: i32): string { return pad + "0123456789abcdef"; }

@noinline
function wr(s: St, text: string): St { strbuf_append(text); return s; }

function main(): i32 {
    strbuf_reset();
    var pad: string = "xyzw";
    var st: St = St { n: 0 };
    var i: i32 = 0;
    while (i < 4) {
        st = wr(st, mk(pad, i));
        i = i + 1;
    }
    var got: string = strbuf_take();
    return (got.len() - 80) + __rc_underflow_count();
}
`,
		skipWasm: "#7867 — strbuf has no wasm implementation",
	},
	{
		// The refusal that keeps the credit sound: one copying use
		// does not launder a retaining one. keep both scans its
		// parameter AND stores it in the array it returns, so
		// everyOccurrenceSafe refuses the whole param and the caller
		// keeps its reference alive for the container. Pinned on the
		// answer + the underflow counter because an over-releasing
		// build reads BETTER on live_bytes.
		// Originally the launder pin from #7867 slice 2: the append store
		// REFUSED the param, and this case pinned the resulting leak to
		// prove the copying-builtin credit did not launder it. The #7914
		// push-element credit made that same append a COUNTED occurrence,
		// so both occurrences are now legitimately safe and the case's job
		// flipped: it proves the two credits COMPOSE and the returned
		// array's element stays live through the caller's read. The
		// refusal it used to watch moved to
		// string_pushed_then_returned_bare_stays_refused below.
		name: "copying_builtin_composes_with_push_credit",
		src: `
@noinline
function mk(pad: string, i: i32): string { return pad + "0123456789abcdef"; }

@noinline
function keep(p: string): string[] {
    var n: i32 = __count_byte(p, 97);
    var out: string[] = [];
    out = out.append(p);
    if (n < 0) { return []; }
    return out;
}

function main(): i32 {
    var pad: string = "xyzw";
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var xs: string[] = keep(mk(pad, i));
        t = t + xs[0].len();
        i = i + 1;
    }
    return (t - 80) + __rc_underflow_count();
}
`,
	},
	{
		// The push credit's REFUSAL half: a param that is pushed AND
		// returned bare has an occurrence nothing counts, so it stays
		// uncredited and the caller's temp keeps its safe leak — pinned
		// in the gate so the refusal is watched (the role the launder
		// pin above used to carry).
		name: "string_pushed_then_returned_bare_stays_refused",
		src: `
@noinline
function mk(pad: string, i: i32): string { return pad + "0123456789abcdef"; }

@noinline
function keep(xs: string[], nm: string): string {
    var ys: string[] = xs.append(nm);
    if (ys.len() > 99) { return "x"; }
    return nm;
}

function main(): i32 {
    var pad: string = "xyzw";
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var base: string[] = [];
        var got: string = keep(base, mk(pad, i));
        t = t + got.len();
        i = i + 1;
    }
    return (t - 80) + __rc_underflow_count();
}
`,
	},
	{
		// A fresh array temp handed to a CONSUMED-THREADED array parameter
		// was owned by nobody. The ownership-flag protocol
		// (emitConsumedArrayOverwriteDec) starts its flag at 0 — the slot
		// still holds the caller's borrow — so the callee never releases
		// the buffer it was handed; and paramCountedRetain refuses the
		// position, because the body hands the parameter out bare
		// (`return out`), so the caller's stage-(b) reclaim declined it
		// too. 16 B a call, the self-host's astwalk fold spine.
		//
		// The second call is the hazard the guard exists for: with an
		// EMPTY item list nothing rebinds `out`, so the callee hands the
		// temp straight back and the result IS the buffer the caller is
		// about to release. Its own reference is still unreclaimed (a
		// call result carries rhsTainted's conservative taint), which is
		// what the pinned bytes are — the point of the row is that the
		// answer is right and the underflow counter is 0, i.e. the guard
		// declined rather than double-freed.
		name: "consumed_array_arg_temp_released_and_guarded",
		src: `
@noinline
function visit(st: string, acc: string[]): string[] { return acc.append(st); }

@noinline
function fold_all(out: string[], items: string[]): string[] {
    var i: i32 = 0;
    while (i < items.len()) { out = visit(items[i], out); i = i + 1; }
    return out;
}

function main(): i32 {
    var items: string[] = ["alpha-item-one", "beta-item-two", "gamma-item-three"];
    var empty: string[] = [];
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 8) {
        var got: string[] = fold_all([], items);
        var none: string[] = fold_all([], empty);
        t = t + got.len() + none.len();
        i = i + 1;
    }
    return (t - 24) + __rc_underflow_count();
}
`,
	},
	{
		// The array dec-on-overwrite has to walk the old buffer's
		// elements wherever that buffer still owns them. The buffer-only
		// __fern_arr_dec is right for exactly one RHS — the self-append,
		// whose MOVE-grow transfers the elements without an inc — and was
		// emitted for every RHS, so both shapes here stranded one element
		// per overwrite (256 B over four rounds on both natives).
		//
		// `a = mk()` cannot alias the old value. `b = via_with(b, ...)`
		// can: the cow hands the receiver's own buffer back at rc 1, so
		// the deep drop sits behind a pointer-changed test and the
		// same-buffer arm keeps the shallow dec that releases what the
		// call added. Dropping that arm rather than guarding it leaks
		// MORE than the original bug — the buffer never reaches zero —
		// which is why both shapes ride in one case.
		name: "array_overwrite_walks_the_superseded_buffer",
		src: `
@noinline
function mk(pad: string, n: i32): string[] {
    var o: string[] = [];
    o = o.append(pad + "-0123456789abcdef");
    return o;
}

@noinline
function via_with(a: string[], v: string): string[] { return a.with(0, v); }

function main(): i32 {
    var pad: string = "wxyz";
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var a: string[] = mk(pad, i);
        a = mk(pad, i + 1);
        var b: string[] = mk(pad, i);
        b = via_with(b, pad + "-fedcba9876543210");
        t = t + a[0].len() + b[0].len();
        i = i + 1;
    }
    return (t - 168) + __rc_underflow_count();
}
`,
	},
	{
		// `xs.with(i, p)` is `xs.append(p)`'s sibling — emitArraySet incs
		// an aliased pointer element and the buffer's deep drop gives it
		// back — but no counted-retain tier had the position, so the
		// caller's fresh argument had no owner left to release it: 32 B a
		// round, and the whole point of the arg-temp stash.
		name: "array_set_element_param_frees_the_caller_temp",
		src: `
@noinline
function put(xs: string[], v: string): string[] { return xs.with(0, v); }

@noinline
function mk(pad: string): string[] {
    var o: string[] = [];
    o = o.append(pad + "-0123456789abcdef");
    return o;
}

function main(): i32 {
    var pad: string = "wxyz";
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var a: string[] = mk(pad);
        var b: string[] = put(a, pad + "-fedcba9876543210");
        t = t + b[0].len();
        i = i + 1;
    }
    return (t - 84) + __rc_underflow_count();
}
`,
	},
	{
		// #7914: a string parameter used only as a CONCAT OPERAND is
		// retained by nothing — strcat copies both operands into a
		// fresh buffer — and until the credit landed that occurrence
		// had no arm. The refusal reached far past the string: the
		// caller's `flags` local was tainted at the call, and
		// rhsTainted's counted-argument check carried that taint into
		// the ARRAY local `reg` is rebound from, so every generation of
		// the registry took the non-freeing __fern_rc_dec. Measured on
		// x86-64: 2752 B stranded over four rounds, 93 allocations
		// against 13 frees; with the credit, 45 allocations and 45
		// frees. arm64 is byte-identical across the change — its
		// two-word string ABI never took the taint — which is what its
		// pinned entry records.
		name: "concat_operand_param_frees_the_caller_array",
		src: `
@noinline
function put(reg: string[], key: string, flags: string): string[] {
    return reg.append(key + "|" + flags);
}

function main(): i32 {
    var keys: string[] = ["alpha-key-one", "beta-key-two", "gamma-key-three"];
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var reg: string[] = [];
        var k: i32 = 0;
        while (k < keys.len()) {
            var flags: string = "";
            var f: i32 = 0;
            while (f < 12) { flags = flags + "1"; f = f + 1; }
            reg = put(reg, keys[k], flags);
            k = k + 1;
        }
        t = t + reg.len();
        i = i + 1;
    }
    return (t - 12) + __rc_underflow_count();
}
`,
	},
	{
		// The same credit through `.with` instead of `.append` — the
		// self-host's own borrow registry, whose buckets are rewritten
		// rather than grown. The x86-64 pin is NOT this credit's
		// residue: it went 4032 -> 384 with it. What is left is a
		// separate defect one layer down — __fern_arr_cow_inplace_ptr
		// INCS each element into the copy it hands back, while the
		// caller's dec-on-overwrite for an array local is the
		// buffer-only __fern_arr_dec, so the old buffer dies without
		// releasing the element references the copy retained. One
		// element per rewritten bucket, and the plain `a = f(...)`
		// overwrite has it too.
		name: "concat_operand_param_rewrites_registry_buckets",
		src: `
@noinline
function put(reg: string[], key: string, flags: string): string[] {
    var b: i32 = key.len() % reg.len();
    return reg.with(b, reg[b] + key + "|" + flags);
}

@noinline
function newreg(): string[] {
    var out: string[] = [];
    var i: i32 = 0;
    while (i < 8) { out = out.append(""); i = i + 1; }
    return out;
}

function main(): i32 {
    var keys: string[] = ["alpha-key-one", "beta-key-two", "gamma-key-three"];
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var reg: string[] = newreg();
        var k: i32 = 0;
        while (k < keys.len()) {
            var flags: string = "";
            var f: i32 = 0;
            while (f < 12) { flags = flags + "1"; f = f + 1; }
            reg = put(reg, keys[k], flags);
            k = k + 1;
        }
        t = t + reg.len();
        i = i + 1;
    }
    return (t - 32) + __rc_underflow_count();
}
`,
	},
	{
		// The own-param interlock: inferParamCountedRetain skips `own`
		// parameters before any classifier runs, and ownedByCalleeAt
		// suppresses the caller-side stash — so a copying use inside
		// an own callee must not become a second release of the same
		// temp.
		name: "copying_builtin_own_param_not_double_freed",
		src: `
@noinline
function mk(pad: string, i: i32): string { return pad + "0123456789abcdef"; }

@noinline
function eat(own text: string): i32 { return __count_byte(text, 97); }

function main(): i32 {
    var pad: string = "xyzw";
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        t = t + eat(pad + "0123456789abcdef");
        i = i + 1;
    }
    return (t - 4) + __rc_underflow_count();
}
`,
	},
	{
		// #7732. A call-bound Option is reboxed
		// (emitRepackPairAsHeapBox), which materialises a REAL rc=1 box
		// for whatever tag the callee returned — but the variant drop
		// plan assumed "payloadless => static sentinel" and gave None no
		// tag arm, so a unique None box fell through the tag switch
		// with nothing freeing it: 32 B per None-returning call,
		// unbounded (measured 300/200, 3200 B live at 200 rounds; the
		// always-Some control was 400/400 clean, which is how the leak
		// hid behind it). Both spellings exit the same, so only the
		// leak gate's zero pin carries the signal — this case exists to
		// put the shape under that pin on all three backends.
		name: "pair_repack_payloadless_box_reclaimed",
		src: `
@noinline
function mk(i: i32): Option[i32[]] { if (i % 2 == 0) { return None; } return Some([i, i + 1]); }

@noinline
function round(i: i32): i32 {
    var o: Option[i32[]] = mk(i);
    match (o) { Some(a) => { return a.len(); }, None => { return 2; } }
    return 0;
}

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 6) { t = t + round(i); i = i + 1; }
    return (t - 12) + __rc_underflow_count();
}
`,
	},
	{
		// #7867 slice 4 / #7914 item 1. A fresh struct's array field
		// projected straight into a call argument —
		// `filter_gate(check_module(...).diags)` — leaked the retained
		// projection and everything it held (608 B/call measured),
		// because the callee indexed its parameter and the blanket
		// Index refusal kept the whole argument reclaim closed. The
		// credit admits ds[i].line (a scalar projection) and
		// out.append(ds[i]) (the push emits the element retain), so the
		// caller's stage-(b) drop fires and the tree balances.
		name: "projected_fresh_field_into_indexing_callee",
		src: `
struct Diag { msg: string, line: i32 }
struct ModuleTypes { diags: Diag[], names: string[], count: i32 }

@noinline
function check_module(n: i32, pad: string): ModuleTypes {
    var ds: Diag[] = [];
    var ns: string[] = [];
    var i: i32 = 0;
    while (i < n) {
        ds = ds.append(Diag { msg: pad + "diagnostic message", line: i });
        ns = ns.append(pad + "name_of_something");
        i = i + 1;
    }
    return ModuleTypes { diags: ds, names: ns, count: n };
}

@noinline
function filter_gate(ds: Diag[]): Diag[] {
    var out: Diag[] = [];
    var i: i32 = 0;
    while (i < ds.len()) {
        if (ds[i].line % 2 == 0) { out = out.append(ds[i]); }
        i = i + 1;
    }
    return out;
}

function main(): i32 {
    var pad: string = "xyzw";
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 3) {
        var gated: Diag[] = filter_gate(check_module(8, pad).diags);
        t = t + gated.len();
        r = r + 1;
    }
    return (t - 12) + __rc_underflow_count();
}
`,
	},
	{
		// #7914 item 2, the field shape. A Map moved into a struct field
		// reached dropStructField's partial chain — value column + buf +
		// handle, no string-KEY column — so every key strand-ed (20/12,
		// 512 B on one round of 8 inserts) while the same map as a bare
		// local balanced. The shared appendMapDropChain walks both
		// columns from every drop site.
		name: "map_field_struct_reclaimed",
		src: `
import "core/map";
import "std/i32";

struct Tbl { m: Map[string, i32], count: i32 }

@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }

@noinline
function build(n: i32): Tbl {
    var m: Map[string, i32] = map_new(8);
    var i: i32 = 0;
    while (i < n) { m = m.insert(w("k") + i.to_string(), i * 3); i = i + 1; }
    return Tbl { m: m, count: n };
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 3) { t = t + build(8).count; r = r + 1; }
    return (t - 24) + __rc_underflow_count();
}
`,
	},
	{
		// The sharing hazard for the same shape: the map is still read
		// through the LOCAL after the struct binds it. Both drops run the
		// full chain and the rc==1 guards arbitrate — whichever owner
		// holds the last reference walks, the other only dec's. An
		// over-release here is an underflow, not a number.
		name: "map_field_shared_live_local",
		src: `
import "core/map";
import "std/i32";

struct Tbl { m: Map[string, i32], count: i32 }

@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }

@noinline
function round(n: i32): i32 {
    var m: Map[string, i32] = map_new(8);
    var i: i32 = 0;
    while (i < n) { m = m.insert(w("k") + i.to_string(), i * 3); i = i + 1; }
    var t: Tbl = Tbl { m: m, count: n };
    return t.count + m.len() + m.get_or(w("k") + "0", 7);
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 3) { t = t + round(8); r = r + 1; }
    return (t - 48) + __rc_underflow_count();
}
`,
	},
	{
		// Struct VALUES under the field drop: the value column routes to
		// the generated __drop_map_via___drop_struct_Pt walk, which
		// deep-drops each value (array field + string field + box) before
		// the key column and the buf + handle go.
		name: "map_field_struct_values_reclaimed",
		src: `
import "core/map";
import "std/i32";

struct Pt { xs: i32[], tag: string }
struct Tbl { m: Map[string, Pt], count: i32 }

@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }

@noinline
function build(n: i32): Tbl {
    var m: Map[string, Pt] = map_new(8);
    var i: i32 = 0;
    while (i < n) {
        m = m.insert(w("k") + i.to_string(), Pt { xs: [i, i + 1], tag: w("t") + i.to_string() });
        i = i + 1;
    }
    return Tbl { m: m, count: n };
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 3) { t = t + build(6).count; r = r + 1; }
    return (t - 18) + __rc_underflow_count();
}
`,
	},
	{
		// #7914 frontier. A fresh string built in argument position for a
		// callee that stores its parameter via `.append` stranded once per
		// call — the checker's derived block-scope shape, one 64 B temp per
		// `child(w(...))` (14/round measured, 44,800 B over 50 rounds).
		// The push element is a COUNTED store, so stringParamCounted now
		// credits the position (the array tier's #7867 slice-1/4 argument)
		// and the caller's stage-(b) release fires. The bare-return
		// hazard keeps its refusal (h_ret_bare probe, leak-safe).
		name: "derived_scope_string_arg_temp_reclaimed",
		src: `
struct Tab { xs: i32[], n: i32 }
struct Env { names: string[], t: Tab, depth: i32 }

@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }

@noinline
function (e: Env) child(nm: string): Env {
    return Env { names: e.names.append(nm), t: e.t, depth: e.depth + 1 };
}

@noinline
function walk(e: Env, d: i32): i32 {
    if (d == 0) { return e.depth + e.t.n + e.names.len(); }
    var acc: i32 = 0;
    acc = acc + walk(e.child(w("a")), d - 1);
    acc = acc + walk(e.child(w("b")), d - 1);
    return acc + e.depth;
}

@noinline
function round(i: i32): i32 {
    var tab: Tab = Tab { xs: [i, i + 1, i + 2], n: i };
    var root: Env = Env { names: [], t: tab, depth: 0 };
    return walk(root, 3) % 97;
}
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 50) { t = t + round(r); r = r + 1; }
    return (t % 89) - 15 + __rc_underflow_count();
}
`,
	},
	{
		// #8104. A read-only accessor that hands back a SUB-OBJECT of its
		// receiver — an element, or the array field itself — used to make
		// the receiver escape, which put it on the owned rung: a caller-side
		// retain and an is_unique-gated deep drop at every call, on a method
		// that reclaims nothing. The projection carries its own unit (the
		// Return lowering incs it), so the receiver borrows now.
		//
		// What the case watches is the drop that credit removes. `b` is the
		// only owner of its cells array; if the returned element were NOT
		// inc'd, borrowing the receiver would leave `everything` and the two
		// Slots pointing into a buffer the round's exit sweep frees, and the
		// next round's writes would land in it. Value-checked at 979 so a
		// stale read fails, __rc_underflow_count() folded in so an
		// over-release does, and leak-gated at 0 on both backends so the
		// removed drop cannot become a leak.
		name: "projection_accessor_receiver_borrows",
		src: `
struct Slot { n: i32, tag: i32 }
struct Bag { cells: Slot[], k: i32 }

@noinline
function (b: Bag) at(i: i32): Slot { return b.cells[i % 3]; }

@noinline
function (b: Bag) all(): Slot[] { return b.cells; }

@noinline
function (b: Bag) via(i: i32): Slot { return b.at(i); }

@noinline
function round(r: i32): i32 {
    var cs: Slot[] = [];
    var i: i32 = 0;
    while (i < 3) { cs = cs.append(Slot { n: r + i, tag: i }); i = i + 1; }
    var b: Bag = Bag { cells: cs, k: r };
    var c: Slot = b.at(r);
    var d: Slot = b.via(r + 1);
    var everything: Slot[] = b.all();
    return c.n + d.tag + everything.len();
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 40) { t = t + round(r); r = r + 1; }
    if (t != 979) { return 1; }
    return __rc_underflow_count();
}
`,
	},
	{
		// A MIXED-return function: fresh on one path, a bare projection of
		// a borrowed parameter on the other. The projection return takes
		// the Return lowering's transfer inc like any other alias, so the
		// caller holds a reference of its own on BOTH paths — which is
		// what lets `s` be reclaimed at all. The hazard the case watches
		// is the other direction: if that credit were ever extended to a
		// return the lowering does not inc, this drop would free a string
		// the array still owns and __rc_underflow_count() would report it.
		name: "mixed_return_param_projection_is_owned",
		src: `
struct Reg { names: string[] }

@noinline
function label(prefix: string, k: i32): string {
    var s: string = prefix + "-";
    var j: i32 = 0;
    while (j < k) { s = s + "xyzw"; j = j + 1; }
    return s;
}

@noinline
function pick(r: Reg, i: i32): string {
    if (i % 7 == 0) { return label("missing", i % 5); }
    return r.names[i % 3];
}

function main(): i32 {
    var ns: string[] = [];
    var b: i32 = 0;
    while (b < 3) { ns = ns.append(label("name", b + 4)); b = b + 1; }
    var r: Reg = Reg { names: ns };
    var i: i32 = 0;
    var n: i32 = 0;
    while (i < 60) {
        var s: string = pick(r, i);
        n = n + s.len();
        i = i + 1;
    }
    return (n % 5) + __rc_underflow_count();
}
`,
	},
	{
		// A lambda in argument position is a fresh pair plus env that the
		// callee only borrows (a function value is never owned-by-default),
		// so the caller is its only owner. freshOwnedRcTempType declined the
		// MakeClosure shape outright and both blocks leaked per call —
		// `m.update(k, (v: i32) => v + i)` in a loop, one pair and one env
		// per iteration (#8057). The scalar-returning `apply` takes the
		// call-level admission; the Box-returning `update` needs the
		// per-position one, which credits a function-typed parameter that
		// is only ever CALLED.
		name: "closure_argument_temp_released",
		src: `
enum Node { Tip, Bin(Node, i32, i32, Node, i32) }
struct Box { root: Node }

@noinline
function find(t: Node, k: i32): Node {
    match (t) {
        Tip => { return t; },
        Bin(l, nk, nv, r, s) => {
            if (k < nk) { return find(l, k); }
            if (k > nk) { return find(r, k); }
            return t;
        }
    }
}

@noinline
function ins(t: Node, k: i32, v: i32): Node {
    match (t) {
        Tip => { return Bin(Tip, k, v, Tip, 1); },
        Bin(l, nk, nv, r, s) => {
            if (k < nk) { var nl: Node = ins(l, k, v); return Bin(nl, nk, nv, r, s + 1); }
            if (k > nk) { var nr: Node = ins(r, k, v); return Bin(l, nk, nv, nr, s + 1); }
            return Bin(l, k, v, r, s);
        }
    }
}

@noinline
function (b: Box) update(k: i32, f: (i32) => i32): Box {
    match (find(b.root, k)) {
        Tip => { return b; },
        Bin(l, nk, nv, r, s) => { return Box { root: ins(b.root, k, f(nv)) }; }
    }
}

@noinline
function apply(x: i32, f: (i32) => i32): i32 { return f(x); }

function main(): i32 {
    var b: Box = Box { root: Tip };
    var i: i32 = 0;
    while (i < 16) { b = Box { root: ins(b.root, i, i) }; i = i + 1; }
    var t: i32 = 0;
    i = 0;
    while (i < 16) {
        b = b.update(i, (v: i32) => v + i);
        t = t + apply(i, (v: i32) => v * 2);
        i = i + 1;
    }
    match (find(b.root, 5)) {
        Tip => { return 1; },
        Bin(l, k, v, r, s) => { t = t + v; }
    }
    return (t - 250) + __rc_underflow_count();
}
`,
	},
	{
		// `?` is an exit, so a move claimed textually AFTER one is a leak on
		// the error path (#8442): the TryOp lowering runs the owned-local dec
		// sweep, and that sweep skips locals marked moved. Both move kinds
		// the dominance guard admits are covered — the bare-ident alias
		// (computeMovedLocals) and the `own` argument (walkDominatingExprs) —
		// and each is driven down BOTH paths so an Ok-only case cannot pass
		// vacuously. Before the fix: 32 bytes live on each Err path, clean on
		// each Ok path.
		name: "move_after_try_op_releases_on_the_err_path",
		src: `
@noinline
function take(own a: i32[]): i32 { return a[0]; }

@noinline
function g(c: i32): Result[i32, i32] {
    if (c == 0) { return Err(7); }
    return Ok(c * 2);
}

@noinline
function aliased(c: i32): Result[i32, i32] {
    var x: i32[] = [1, 2, 3];
    var r: i32 = g(c)?;
    var y: i32[] = x;
    return Ok(y[0] + r);
}

@noinline
function owned(own a: i32[], c: i32): Result[i32, i32] {
    var r: i32 = g(c)?;
    return Ok(take(a) + r);
}

function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        match (aliased(0)) {
            Ok(v) => { acc = acc + 1000; },
            Err(e) => { acc = acc + e; }
        }
        match (aliased(3)) {
            Ok(v) => { acc = acc + v; },
            Err(e) => { acc = acc + 1000; }
        }
        match (owned([4, 5, 6], 0)) {
            Ok(v) => { acc = acc + 1000; },
            Err(e) => { acc = acc + e; }
        }
        match (owned([4, 5, 6], 3)) {
            Ok(v) => { acc = acc + v; },
            Err(e) => { acc = acc + 1000; }
        }
        i = i + 1;
    }
    return (acc - 1550) + __rc_underflow_count();
}`,
	},
	{
		// A closure CYCLE must leak, not crash. `var f = () => g(); g = f;`
		// makes the mutated capture's boxed cell hold the very pair that is
		// being released, and refcounts cannot collect that — leaking is the
		// correct outcome, and the one #8440 documents while the checker hole
		// that admits the cycle stays open.
		//
		// #8545 made the per-closure thunk reachable for such a local, and
		// the thunk dispatched into __drop_arr_closure, which dispatched back
		// into the thunk: unbounded recursion, SIGSEGV on all three backends
		// (#8637). Routing that arm through the flat per-element dec instead
		// traded the crash for `rc over-release (double free)` — on a cycle
		// the counts are already wrong, so ANY release is. The thunk now
		// leaves a closure-typed capture alone and frees only the env.
		//
		// Against the #8545 compiler this case dies with a signal, which the
		// corpus reads as a crash rather than a verdict; before #8545 it
		// leaked 3200 bytes where it now leaks 1600.
		name: "closure_cycle_leaks_without_crashing",
		src: `
@noinline
function round(n: i32): i32 {
    var g: () => i32 = function (): i32 { return 1; };
    var f: () => i32 = function (): i32 { return g() + n; };
    g = f;
    return n;
}

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { t = t + round(i); i = i + 1; }
    return (t - 1225) + __rc_underflow_count();
}`,
	},
	{
		// A closure LOCAL handed to a callee keeps its pair: the slot has a
		// reader ElideClosurePair cannot elide, so the exit sweep's
		// per-closure thunk — which reads a bare env — cannot run on the
		// slot directly. It is dispatched through the drop-fn pointer the
		// pair carries instead (#8545), which reaches the same thunk with
		// the env. Both the named nested function and the lambda bound to
		// a local reclaim their pair, env and captures; the leak gates
		// hold this at zero on all three backends.
		name: "closure_local_passed_to_callee_released",
		src: `
@noinline
function each(n: i32, f: (i32) => i32): void {
    var i: i32 = 0;
    while (i < n) { f(i); i = i + 1; }
}

@noinline
function run_named(): i32 {
    var seen: i32 = 0;
    function visit(x: i32): i32 {
        seen = seen * 10 + x;
        return seen;
    }
    each(3, visit);
    return seen;
}

@noinline
function run_lambda_local(): i32 {
    var seen: i32 = 0;
    var visit: (i32) => i32 = (x: i32) => { seen = seen * 10 + x; seen };
    each(3, visit);
    return seen;
}

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) { t = t + run_named() + run_lambda_local(); i = i + 1; }
    return (t - 96) + __rc_underflow_count();
}
`,
	},
	{
		// The scalar-capture twin of the case above (#8546). A closure whose
		// captures are all scalar has no rc-tracked capture, so emitDec drops
		// its local through the generic __fern_closure_drop, which frees the
		// one block it is handed. On a slot that does not elide that block
		// is the PAIR, not the env, so the env leaked whenever such a
		// closure crossed a call as an argument — value-returning inside a
		// return expression and void statement spelling alike. The same
		// closure called directly elides to a bare env and was always
		// clean; it rides along as the control.
		name: "closure_scalar_capture_passed_to_callee_released",
		src: `
@noinline
function apply(f: (i32) => i32, v: i32): i32 { return f(v); }

@noinline
function run(f: (i32) => void, v: i32): void { f(v); }

@noinline
function returning(): i32 {
    var sink: i32 = 3;
    var add = (x: i32) => sink + x;
    return apply(add, 4) - 4;
}

@noinline
function statement(): i32 {
    var sink: i32 = 0;
    var log = function (x: i32): void { sink = sink + x * 2; };
    run(log, 4);
    return sink - 8;
}

@noinline
function direct(): i32 {
    var sink: i32 = 0;
    var log = (x: i32) => { sink = sink + x * 2; };
    log(4);
    return sink - 8;
}

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) { t = t + returning() + statement() + direct(); i = i + 1; }
    return (t - 12) + __rc_underflow_count();
}
`,
	},
	{
		// A tree walk's `Tip => return t` arm returns its OWNED-BY-DEFAULT
		// parameter bare: the caller retained the argument on the way in
		// and the callee's sweep releases it, so the return-transfer inc is
		// the caller's own. returnsOwnBox refused every bare parameter, so
		// `filter` never counted as fresh and each caller's `fl` / `fr`
		// binding kept the conservative call taint — the nodes `join`
		// rebuilt around them were never released (std/ordmap union /
		// filter: 960 and 1,368 blocks on 1,000-entry maps, #8057).
		name: "tree_rebuild_returning_owned_param_reclaimed",
		src: `
enum Node { Tip, Bin(Node, i32, i32, Node, i32) }

@noinline
function size(t: Node): i32 {
    match (t) {
        Tip => { return 0; },
        Bin(l, k, v, r, s) => { return s; }
    }
}

@noinline
function mk(l: Node, k: i32, v: i32, r: Node): Node {
    return Bin(l, k, v, r, size(l) + size(r) + 1);
}

@noinline
function ins(t: Node, k: i32, v: i32): Node {
    match (t) {
        Tip => { return Bin(Tip, k, v, Tip, 1); },
        Bin(l, nk, nv, r, s) => {
            if (k < nk) { var nl: Node = ins(l, k, v); return mk(nl, nk, nv, r); }
            if (k > nk) { var nr: Node = ins(r, k, v); return mk(l, nk, nv, nr); }
            return Bin(l, k, v, r, s);
        }
    }
}

@noinline
function insert_min(k: i32, v: i32, t: Node): Node {
    match (t) {
        Tip => { return Bin(Tip, k, v, Tip, 1); },
        Bin(l, nk, nv, r, s) => {
            var nl: Node = insert_min(k, v, l);
            return mk(nl, nk, nv, r);
        }
    }
}

@noinline
function join(l: Node, k: i32, v: i32, r: Node): Node {
    match (l) {
        Tip => { return insert_min(k, v, r); },
        Bin(ll, lk, lv, lr, ls) => { return mk(l, k, v, r); }
    }
}

@noinline
function filter(t: Node, keep_even: boolean): Node {
    match (t) {
        Tip => { return t; },
        Bin(l, k, v, r, s) => {
            var fl: Node = filter(l, keep_even);
            var fr: Node = filter(r, keep_even);
            if (k % 2 == 0) {
                return join(fl, k, v, fr);
            }
            return join(fr, k, v, fl);
        }
    }
}

function main(): i32 {
    var t: Node = Tip;
    var i: i32 = 0;
    while (i < 64) { t = ins(t, (i * 37) % 64, i); i = i + 1; }
    var f: Node = filter(t, true);
    return (size(f) - 64) + __rc_underflow_count();
}
`,
	},
	{
		// A fresh struct temp passed to a POINTER-returning method —
		// `s.concat(mk(2))` — has only the per-position admission to
		// release it, which asks whether the callee retains that parameter
		// counted. `other.tail[0]` and `other.get_or(i, …)` were refused:
		// a scalar element read out of an array field, and the receiver of
		// a method whose own reads are the same shape. Two blocks per call
		// (the struct box and its array) until both reads counted as the
		// value copies they are (#8057).
		name: "fresh_temp_argument_to_pointer_returning_method_released",
		src: `
struct Vec { len: i32, tail: i32[] }

@noinline
function mk(n: i32): Vec {
    var xs: i32[] = [];
    var i: i32 = 0;
    while (i < n) { xs = xs.append(i); i = i + 1; }
    return Vec { len: n, tail: xs };
}

@noinline
function (v: Vec) get_or(i: i32, fallback: i32): i32 {
    if (i < 0 || i >= v.len) { return fallback; }
    return v.tail[i];
}

@noinline
function (v: Vec) append(x: i32): Vec {
    return Vec { len: v.len + 1, tail: v.tail.append(x) };
}

@noinline
function (v: Vec) concat(other: Vec): Vec {
    var out: Vec = v;
    var i: i32 = 0;
    while (i < other.len) { out = out.append(other.get_or(i, other.tail[0])); i = i + 1; }
    return out;
}

function main(): i32 {
    var s: Vec = mk(3);
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 4) { var c: Vec = s.concat(mk(2)); t = t + c.len; i = i + 1; }
    return (t - 20) + __rc_underflow_count();
}
`,
	},
	{
		// #8056: an array-carrying enum is owned by default, its consuming
		// match hands the child array to the binding, and a self-reassign
		// argument is MOVED into the owned position — so a uniquely held
		// trie is rewritten in place on the unique path. The take-out
		// (`kids.with(sub, E)` before the descent) is the library idiom that
		// keeps the child unique too. Value-correct after 40 rounds; the
		// leak gate pins it at zero.
		name: "array_payload_enum_unique_path_in_place",
		src: `
enum N { E, L(i32[]), B(N[]) }
function with_in(node: N, shift: i32, i: i32, x: i32): N {
    match (node) {
        B(kids) => {
            var sub: i32 = i >> shift & 31;
            var child: N = kids[sub];
            var rest: N[] = kids.with(sub, E);
            child = with_in(child, shift - 5, i, x);
            return B(rest.with(sub, child));
        },
        L(xs) => { return L(xs.with(i & 31, x)); },
        E => { return E; }
    }
}
function get(node: N, shift: i32, i: i32): i32 {
    match (node) {
        B(kids) => { return get(kids[i >> shift & 31], shift - 5, i); },
        L(xs) => { return xs[i & 31]; },
        E => { return -1; }
    }
}
function build(): N {
    var kids: N[] = [];
    var a: i32 = 0;
    while (a < 4) {
        var xs: i32[] = [];
        var b: i32 = 0;
        while (b < 32) { xs = xs.append(a * 32 + b); b = b + 1; }
        kids = kids.append(L(xs));
        a = a + 1;
    }
    return B(kids);
}
function main(): i32 {
    var t: N = build();
    var r: i32 = 0;
    while (r < 40) {
        var j: i32 = 0;
        while (j < 128) { t = with_in(t, 5, j, j + r); j = j + 1; }
        r = r + 1;
    }
    var sum: i32 = 0;
    var i: i32 = 0;
    while (i < 128) { sum = sum + get(t, 5, i); i = i + 1; }
    return (sum - 13120) + __rc_underflow_count();
}`,
	},
	{
		// The shared path of the same trie: two snapshots are held across
		// two rounds of updates and must read back unchanged, while the
		// value that was unique after the first copy goes on in place.
		name: "array_payload_enum_shared_path_copies",
		src: `
enum N { E, L(i32[]), B(N[]) }
function with_in(node: N, shift: i32, i: i32, x: i32): N {
    match (node) {
        B(kids) => {
            var sub: i32 = i >> shift & 31;
            var child: N = kids[sub];
            var rest: N[] = kids.with(sub, E);
            child = with_in(child, shift - 5, i, x);
            return B(rest.with(sub, child));
        },
        L(xs) => { return L(xs.with(i & 31, x)); },
        E => { return E; }
    }
}
function get(node: N, shift: i32, i: i32): i32 {
    match (node) {
        B(kids) => { return get(kids[i >> shift & 31], shift - 5, i); },
        L(xs) => { return xs[i & 31]; },
        E => { return -1; }
    }
}
function build(): N {
    var kids: N[] = [];
    var a: i32 = 0;
    while (a < 4) {
        var xs: i32[] = [];
        var b: i32 = 0;
        while (b < 32) { xs = xs.append(a * 32 + b); b = b + 1; }
        kids = kids.append(L(xs));
        a = a + 1;
    }
    return B(kids);
}
function main(): i32 {
    var t: N = build();
    var snap: N = t;
    var j: i32 = 0;
    while (j < 128) { t = with_in(t, 5, j, j + 7); j = j + 1; }
    var snap2: N = t;
    j = 0;
    while (j < 128) { t = with_in(t, 5, j, j + 9); j = j + 1; }
    var sum: i32 = 0;
    var s1: i32 = 0;
    var s2: i32 = 0;
    var i: i32 = 0;
    while (i < 128) {
        sum = sum + get(t, 5, i);
        s1 = s1 + get(snap, 5, i);
        s2 = s2 + get(snap2, 5, i);
        i = i + 1;
    }
    if (s1 != 8128) { return 1; }
    if (s2 != 9024) { return 2; }
    return (sum - 9280) + __rc_underflow_count();
}`,
	},
	{
		// The HAMT shape: a NON-uniform enum (Leaf is a 16-byte box, Br 24)
		// is owned by default too — each consuming arm frees the box at its
		// own variant's size — and the wrapper takes the root out of the
		// handle before descending.
		name: "non_uniform_enum_owned_consuming_match",
		src: `
struct CK { bucket: i32, id: i32 }
enum H { E, L(i32, CK, i32), Br(i32, H[]) }
struct PM { root: H }
function ck_eq(a: CK, b: CK): boolean { return a.bucket == b.bucket && a.id == b.id; }
function ins(n: H, h: i32, shift: i32, k: CK, v: i32): H {
    match (n) {
        E => { return L(h, k, v); },
        L(lh, lk, lv) => {
            if (lh == h && ck_eq(lk, k)) { return L(h, k, v); }
            var kids: H[] = [L(lh, lk, lv), L(h, k, v)];
            return Br(0, kids);
        },
        Br(bm, kids) => {
            var idx: i32 = h >> shift & 3;
            if (idx >= kids.len()) { return Br(bm, kids.append(L(h, k, v))); }
            var child: H = kids[idx];
            var rest: H[] = kids.with(idx, E);
            child = ins(child, h, shift + 2, k, v);
            return Br(bm, rest.with(idx, child));
        }
    }
}
function find(n: H, h: i32, shift: i32, k: CK): i32 {
    match (n) {
        E => { return -1; },
        L(lh, lk, lv) => { if (ck_eq(lk, k)) { return lv; } return -1; },
        Br(bm, kids) => {
            var idx: i32 = h >> shift & 3;
            if (idx >= kids.len()) { return -1; }
            return find(kids[idx], h, shift + 2, k);
        }
    }
}
function (m: PM) insert(k: CK, v: i32): PM {
    var root: H = m.root;
    m = PM { ...m, root: E };
    root = ins(root, k.bucket, 0, k, v);
    return PM { ...m, root: root };
}
function main(): i32 {
    var c: PM = PM { root: E };
    var i: i32 = 0;
    while (i < 64) { c = c.insert(CK { bucket: i, id: i }, i); i = i + 1; }
    var r: i32 = 0;
    while (r < 5) {
        var j: i32 = 0;
        while (j < 64) { c = c.insert(CK { bucket: j, id: j }, j + r); j = j + 1; }
        r = r + 1;
    }
    var sum: i32 = 0;
    i = 0;
    while (i < 64) { sum = sum + find(c.root, i, 0, CK { bucket: i, id: i }); i = i + 1; }
    return (sum - 2272) + __rc_underflow_count();
}`,
	},
	{
		// A string-carrying enum is owned by default: the rebuilt value and
		// a snapshot both read back right.
		name: "string_payload_enum_owned",
		src: `
enum S { A(string, i32), Z }
function bump(s: S, tag: string): S {
    match (s) {
        A(txt, n) => { return A(txt + tag, n + 1); },
        Z => { return Z; }
    }
}
function len_of(s: S): i32 {
    match (s) {
        A(txt, n) => { return txt.len() + n; },
        Z => { return 0; }
    }
}
function main(): i32 {
    var s: S = A("x", 0);
    var i: i32 = 0;
    while (i < 200) { s = bump(s, "y"); i = i + 1; }
    var snap: S = s;
    s = bump(s, "zz");
    if (len_of(snap) != 401) { return 1; }
    return (len_of(s) - 404) + __rc_underflow_count();
}`,
	},
	{
		// `.append` on an array bound out of a NON-consuming match of a
		// borrowed enum grew the caller's buffer in place when it had room
		// (`.with` had the guard, `.append` did not): 33 is right, 34 was
		// the bug. Interpreter and natives now agree.
		name: "append_on_borrowed_match_binding_copies",
		src: `
enum N { L(i32), B(i32, N[]) }
function grow(n: N, v: i32): N {
    match (n) {
        L(x) => { return n; },
        B(c, kids) => { return B(c + 1, kids.append(L(v))); }
    }
}
function count(n: N): i32 {
    match (n) {
        L(x) => { return 1; },
        B(c, kids) => { return kids.len(); }
    }
}
function main(): i32 {
    var ks: N[] = [];
    ks = ks.append(L(1));
    ks = ks.append(L(2));
    ks = ks.append(L(3));
    var a: N = B(0, ks);
    var before: i32 = count(a);
    var c: N = grow(a, 4);
    var after: i32 = count(a);
    return (before * 10 + after - 33) + __rc_underflow_count();
}`,
	},
	{
		// A struct passed as a FIELD argument (`push(h.b)`) was never
		// bracketed by the #4873 containment, so the callee's rc==1 append
		// grew `h.b.xs` in place. The bracket now follows the field chain.
		name: "struct_field_arg_append_bracketed",
		src: `
struct Box { xs: i32[] }
struct Holder { b: Box }
function push(b: Box, x: i32): Box {
    var ys: i32[] = b.xs.append(x);
    return Box { xs: ys };
}
function size(b: Box): i32 { return b.xs.len(); }
function main(): i32 {
    var xs: i32[] = [];
    xs = xs.append(1);
    xs = xs.append(2);
    xs = xs.append(3);
    var h: Holder = Holder { b: Box { xs: xs } };
    var before: i32 = size(h.b);
    var c: Box = push(h.b, 4);
    var after: i32 = size(h.b);
    return (before * 10 + after - 33) + __rc_underflow_count();
}`,
	},
	{
		// An owned array local whose textually-last use is a consuming
		// `.with`, on a function that can return before reaching it: the
		// consuming site nulls the slot, so the early path still releases
		// the buffer (it used to be skipped on every path — 150 blocks
		// stranded here). Leak-only; the leak gate holds it at zero.
		name: "with_receiver_released_on_early_return",
		src: `
enum N { L(i32), B(i32, N[]) }
function mk(c: i32, flag: boolean): N {
    var ks: N[] = [];
    ks = ks.append(L(1));
    ks = ks.append(L(2));
    if (flag) { return L(c); }
    return B(c, ks.with(0, L(c)));
}
function count(n: N): i32 {
    match (n) {
        L(x) => { return 1; },
        B(c, kids) => { return kids.len(); }
    }
}
function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var n: N = mk(i, i % 2 == 0);
        total = total + count(n);
        i = i + 1;
    }
    return (total - 150) + __rc_underflow_count();
}`,
	},
	{
		// The tree-walk read path (`var cur = root; cur = kids[..]`) taints
		// `root` through the alias, which used to keep an owned parameter's
		// count forever: one whole trie stranded per lookup. An owned
		// parameter now spends its count unless it ESCAPED into an uncounted
		// sink. Leak-only; the leak gate holds it at zero.
		name: "owned_param_alias_walk_releases_count",
		src: `
enum N { E, L(i32[]), B(N[]) }
struct PV { len: i32, root: N, tail: i32[] }
function leaf_for(root: N, shift: i32, i: i32): i32[] {
    var cur: N = root;
    var level: i32 = shift;
    var descending: boolean = true;
    while (descending) {
        match (cur) {
            B(kids) => { cur = kids[i >> level & 31]; level = level - 5; },
            L(xs) => { return xs; },
            E => { descending = false; }
        }
    }
    var none: i32[] = [];
    return none;
}
function (v: PV) get_or(i: i32, fallback: i32): i32 {
    if (i < 0 || i >= v.len) { return fallback; }
    var leaf: i32[] = leaf_for(v.root, 5, i);
    return leaf[i & 31];
}
function build(): PV {
    var kids: N[] = [];
    var a: i32 = 0;
    while (a < 4) {
        var xs: i32[] = [];
        var b: i32 = 0;
        while (b < 32) { xs = xs.append(a * 32 + b); b = b + 1; }
        kids = kids.append(L(xs));
        a = a + 1;
    }
    var t: i32[] = [];
    t = t.append(7);
    return PV { len: 128, root: B(kids), tail: t };
}
function main(): i32 {
    var v: PV = build();
    var sum: i32 = 0;
    var i: i32 = 0;
    while (i < 128) { sum = sum + v.get_or(i, -1); i = i + 1; }
    return (sum - 8128) + __rc_underflow_count();
}`,
	},
	{
		// `root = ins(root, .., k, v)` carries k into root, which is
		// returned: the escape analysis followed only `var` initialisers,
		// so k read as borrowable and the caller's fresh key temp was never
		// released (one key box per insert). Leak-only.
		name: "param_escapes_through_assignment",
		src: `
struct CK { bucket: i32, id: i32 }
enum H { E, L(i32, CK, i32) }
struct PM { root: H }
function ins(n: H, k: CK, v: i32): H {
    match (n) {
        E => { return L(0, k, v); },
        L(lh, lk, lv) => { return L(lh, k, v); }
    }
}
function (m: PM) insert(k: CK, v: i32): PM {
    var root: H = m.root;
    m = PM { ...m, root: E };
    root = ins(root, k, v);
    return PM { ...m, root: root };
}
function main(): i32 {
    var c: PM = PM { root: E };
    var i: i32 = 0;
    while (i < 50) { c = c.insert(CK { bucket: i, id: i }, i); i = i + 1; }
    match (c.root) {
        E => { return 1; },
        L(lh, lk, lv) => { return (lk.id - 49) + __rc_underflow_count(); }
    }
}`,
	},
	{
		// A closure forwards its captured struct to a callee that keeps the
		// param (owned), and runs more than once: the env's reference is
		// retained at each call, or the second run reads a freed ctx.
		name: "closure_capture_passed_to_owned_param",
		src: `
struct Ctx { decls: string[] }
struct Txn { headers: string[] }
struct Out { ctx: Ctx, txn: Txn, n: i32 }
function run_sub(ctx: Ctx, t: Txn, name: string): Out {
    var hs: string[] = t.headers.append(name);
    return Out { ctx: ctx, txn: Txn { headers: hs }, n: hs.len() };
}
function driver(decls: string[]): (string, Txn) => Out {
    var ctx: Ctx = Ctx { decls: decls };
    var runner: (string, Txn) => Out = (name: string, t: Txn): Out => { return run_sub(ctx, t, name); };
    return runner;
}
function main(): i32 {
    var run: (string, Txn) => Out = driver(["a", "b"]);
    var t: Txn = Txn { headers: ["h"] };
    var i: i32 = 0;
    var total: i32 = 0;
    while (i < 4) {
        var out: Out = run("s", t);
        t = out.txn;
        total = total + out.n + out.ctx.decls.len();
        i = i + 1;
    }
    return (total - 22) + __rc_underflow_count();
}`,
	},
	{
		// #7867 class C: a fresh array temp handed to a callee that appends
		// to its parameter and returns the result without reassigning the
		// parameter. The receiver of an append is a counted occurrence —
		// the grow bumps the buffer to rc 2 before handing it back in
		// place, and leaves it at rc 1 beside a fresh copy otherwise — so
		// the position is counted-retain: the caller releases the temp
		// unconditionally after the call and credits the binding of the
		// result. `seed()` is the identity path: its buffer has spare
		// capacity, so the callee grows it in place and returns the very
		// temp the caller then releases — at rc 2, down to the result's
		// one. Nothing was released before (400 allocs / 0 frees on the
		// i32 form), and a pointer-guarded release would have stranded the
		// identity path.
		name: "append_receiver_param_arg_temp_released",
		src: `
@noinline
function acc_i(xs: i32[], s: i32): i32[] { return xs.append(s); }
@noinline
function acc_s(xs: string[], s: string): string[] { return xs.append(s); }
@noinline
function acc_chain(xs: i32[], s: i32): i32[] { return xs.append(s).append(s + 1); }
@noinline
function seed(): i32[] { var s: i32[] = []; s = s.append(1); return s; }

function main(): i32 {
    var pad: string = "wide-payload-";
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 8) {
        var ys: i32[] = acc_i([], i);
        var zs: i32[] = acc_i([1, 2], i);
        var ss: string[] = acc_s([], pad + "x");
        var ws: string[] = acc_s([pad + "a", pad + "b"], pad + "y");
        var ch: i32[] = acc_chain([], i);
        var sd: i32[] = acc_i(seed(), i);
        if (sd[0] != 1) { return 90; }
        if (sd[1] != i) { return 91; }
        if (ch[1] != i + 1) { return 92; }
        t = t + ys.len() + zs.len() + ss.len() + ws.len() + ch.len() + sd.len();
        i = i + 1;
    }
    return (t - 96) + __rc_underflow_count();
}
`,
	},
	{
		// The same callee with a LIVE caller local at the position: the
		// #4873 bracket holds the local at rc 2 across the call, so the
		// callee's return-position append copies and the caller's buffer —
		// which has spare capacity and would otherwise grow in place — is
		// unchanged. The result is therefore fresh and its binding is
		// credited; the local is not a temp and is not released at the
		// call. Value semantics are the check: `g` stays at three elements
		// after two appends through the callee.
		name: "append_receiver_param_live_local_keeps_value",
		src: `
@noinline
function acc_i(xs: i32[], s: i32): i32[] { return xs.append(s); }
@noinline
function acc_chain(xs: i32[], s: i32): i32[] { return xs.append(s).append(s + 1); }

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 8) {
        var g: i32[] = [];
        g = g.append(1);
        g = g.append(2);
        g = g.append(3);
        var a: i32[] = acc_i(g, i);
        var b: i32[] = acc_i(g, i + 1);
        var c: i32[] = acc_chain(g, i);
        if (g.len() != 3) { return 90; }
        if (a[3] != i) { return 91; }
        if (b[3] != i + 1) { return 92; }
        if (c.len() != 5) { return 93; }
        t = t + a.len() + b.len() + c.len();
        i = i + 1;
    }
    return (t - 104) + __rc_underflow_count();
}
`,
	},
	{
		// An append whose receiver is itself an owned temp — the inner
		// result of a chain, a literal, a fresh call result — consumes that
		// reference: released after the outer grow, where before it was
		// stranded at rc 2 (the inner grew in place, so the outer copied
		// at rc 2 and nothing released the original) or at rc 1 (the inner
		// copied). `x = x.append(k).append(k + 1)` leaked eight of its nine
		// buffers per round. The string chain exercises the deep drop: the
		// outer grow's copy path retains the elements, so the superseded
		// buffer still owns its own and the walk is right.
		name: "append_chain_intermediate_released",
		src: `
@noinline
function mk(p: string): string[] { return [p + "seed"]; }

function main(): i32 {
    var pad: string = "wide-payload-";
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 8) {
        var x: i32[] = [];
        var k: i32 = 0;
        while (k < 6) { x = x.append(k).append(k + 1); k = k + 1; }
        var a: i32[] = [7].append(i);
        var b: string[] = mk(pad).append(pad + "lit").append(pad + "more");
        if (x[11] != 6) { return 90; }
        if (a[0] != 7) { return 91; }
        if (a[1] != i) { return 92; }
        if (b[2].len() != 17) { return 93; }
        t = t + x.len() + a.len() + b.len();
        i = i + 1;
    }
    return (t - 136) + __rc_underflow_count();
}
`,
	},
	{
		// #7914. `T { ...mk(), f: v }` releases its spread base only when
		// structUpdateBaseIsOwned admits it, and its Call arm asked for
		// whole-program escape freedom — which a registry builder never has,
		// because one field holding a parameter refuses the whole function.
		// `mkreg` is exactly that: `names: seed` puts a parameter in the
		// result, so `returnsNoParamEscape` is false while the returned box is
		// plainly fresh. Before the fresh-box arm the base box, its overridden
		// `tag` string and its `names` retain all stranded — 256 B on x86-64
		// and 304 on arm64 over three rounds, unbounded.
		//
		// `seed` stays live across every round and is read through the copy,
		// so this pins the sharing hazard the release must not disturb: the
		// non-overridden field is inc'd into the new box and the base's deep
		// drop nets it back, rather than freeing the caller's array.
		//
		// @noinline on both is load-bearing — an inlined producer leaves no
		// base temp and the shape reads clean either way.
		name: "struct_update_base_fresh_call_result_released",
		src: `
struct Reg { names: string[], tag: string, n: i32 }

@noinline
function mkreg(seed: string[], pad: string): Reg {
    return Reg { names: seed, tag: pad + "0123456789abcdef", n: seed.len() };
}

@noinline
function widen(r: Reg): i32 { return r.names.len() + r.tag.len() + r.n; }

function main(): i32 {
    var pad: string = "xyzw";
    var seed: string[] = [pad + "0123456789abcdef"];
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var r: Reg = Reg { ...mkreg(seed, pad), tag: pad + "fedcba9876543210" };
        t = t + widen(r);
        i = i + 1;
    }
    return (t - 66) + __rc_underflow_count();
}`,
	},
	{
		// The interlock for the case above: the base is the CALLER's live
		// struct, handed straight back by a passthrough. `returnsOwnBox`
		// credits the bare returned parameter because it is owned-by-default
		// — the caller retained it on the way in — so the base is released
		// here, and `base` must survive it. Every round reads through both
		// the copy and `base`, and the post-loop read pins the string
		// contents rather than just the shape.
		name: "struct_update_base_passthrough_keeps_caller_box",
		src: `
struct Reg2 { names: string[], tag: string, n: i32 }

@noinline
function thread(r: Reg2): Reg2 { return r; }

function main(): i32 {
    var pad: string = "xyzw";
    var base: Reg2 = Reg2 { names: [pad + "0123456789abcdef"], tag: pad + "0123456789abcdef", n: 1 };
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var r: Reg2 = Reg2 { ...thread(base), tag: pad + "fedcba9876543210" };
        t = t + r.names.len() + r.tag.len() + base.names.len() + base.tag.len();
        i = i + 1;
    }
    var last: i32 = base.names[0].len() + base.tag.len();
    return (t - 126) + (last - 40) + __rc_underflow_count();
}`,
	},
	{
		// #8186: `a = Asm { ...a, cfi: record(a.cfi, v) }` with
		// `record(own s: Cfi, …)` MOVES the field out of a's box into the
		// callee — the store supersedes it — instead of E051 refusing the
		// shape. The call site tests is_unique(a): a unique box has its slot
		// emptied (every later release meets a null), a shared one keeps
		// its field and retains the value for the callee. Rebind and return
		// forms, an own-param base and a local base, an alias that must
		// keep reading its field, and the loop the shape is written for.
		name: "struct_update_field_move_into_own_param",
		src: `
struct Cfi { rules: i32[], n: i32 }
struct Asm { code: i32[], cfi: Cfi }
function record(own s: Cfi, v: i32): Cfi { return Cfi { rules: s.rules.append(v), n: s.n + 1 }; }
function step(own a: Asm, v: i32): Asm {
    a = Asm { ...a, cfi: record(a.cfi, v) };
    a = Asm { ...a, code: a.code.append(v) };
    return a;
}
function step_ret(own a: Asm, v: i32): Asm { return Asm { ...a, cfi: record(a.cfi, v) }; }
function shared(v: i32): i32 {
    var a: Asm = Asm { code: [], cfi: Cfi { rules: [1, 2], n: 2 } };
    var keep: Asm = a;
    a = Asm { ...a, cfi: record(a.cfi, v) };
    return keep.cfi.n * 100 + a.cfi.n + keep.cfi.rules.len() * 1000;
}
function local_form(n: i32): i32 {
    var a: Asm = Asm { code: [], cfi: Cfi { rules: [], n: 0 } };
    var i: i32 = 0;
    while (i < n) {
        a = Asm { ...a, cfi: record(a.cfi, i) };
        i = i + 1;
    }
    return a.cfi.n + a.cfi.rules[n - 1];
}
function main(): i32 {
    var a: Asm = Asm { code: [], cfi: Cfi { rules: [], n: 0 } };
    var i: i32 = 0;
    while (i < 200) {
        a = step(a, i);
        a = step_ret(a, i);
        i = i + 1;
    }
    var bad: i32 = 0;
    if (a.cfi.n != 400) { bad = bad + 1; }
    if (a.cfi.rules.len() != 400) { bad = bad + 2; }
    if (a.code.len() != 200) { bad = bad + 4; }
    if (a.cfi.rules[399] != 199) { bad = bad + 8; }
    if (a.code[199] != 199) { bad = bad + 16; }
    if (shared(7) != 2203) { bad = bad + 32; }
    if (local_form(50) != 99) { bad = bad + 64; }
    return bad + __rc_underflow_count();
}`,
	},
	{
		// #8406: a `[T]` slice header is an rc1 block the IR releases like a
		// tuple box — the exit sweep for a local, the arg-temp release for
		// `f(s.as_bytes())`, the receiver stash for `.len()` / `[i]`, the
		// cast for `as usize` (std/string.bytes), and the parent of a
		// sub-slice. Every consumer shape in one 10k loop; before the
		// header carried an rc it bumped 16 bytes per call that nothing
		// could free. Heap-form strings only: `as_bytes` on an inline
		// string promotes the bytes into an unowned copy, a separate gap.
		name: "slice_header_churn_free",
		src: `
import "std/string";
function sum_view(b: [u8]): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < b.len()) { t = t + (b[i] as i32); i = i + 1; }
    return t;
}
function main(): i32 {
    var s: string = "hello world, this is a heap string";
    var want_sum: i32 = 0;
    var k: i32 = 0;
    while (k < s.len()) { want_sum = want_sum + (s[k] as i32); k = k + 1; }
    var per: i32 = (s[0] as i32) + want_sum + s.len() + (s[1] as i32) + (s[1] as i32) + s.len();
    var xs: i32[] = [10, 20, 30, 40, 50];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 10000) {
        var b: [u8] = s.as_bytes();
        acc = acc + (b[0] as i32);
        acc = acc + sum_view(s.as_bytes());
        acc = acc + s.as_bytes().len();
        acc = acc + (s.as_bytes()[1] as i32);
        var c: [u8] = b[1:3];
        acc = acc + (c[0] as i32);
        var copy: u8[] = s.bytes();
        acc = acc + copy.len();
        var v: [i32] = xs[1:4];
        var w: [i32] = v[1:2];
        acc = acc + v[0] + w[0] + xs[2:5].len();
        i = i + 1;
    }
    if (acc != 10000 * (per + 53)) { return 1; }
    return __rc_underflow_count();
}`,
	},
	{
		// A sub-slice views the SOURCE's bytes, never its parent header, so
		// the parent may be released first: reassigned while the child is
		// live, dropped at a callee's exit while the child is returned, or
		// released as the temp the child was cut from. The child stays
		// valid and the parent's header is reclaimed each time.
		name: "slice_sub_view_outlives_parent_header",
		src: `
function tail(s: string): [u8] {
    var a: [u8] = s.as_bytes();
    var b: [u8] = a[1:3];
    return b;
}
function main(): i32 {
    var s: string = "hello world, this is a heap string";
    var t: string = "another heap string, also long";
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 1000) {
        var a: [u8] = s.as_bytes();
        var b: [u8] = a[1:3];
        a = t.as_bytes();
        if (b.len() != 2 || (b[0] as i32) != 101 || (b[1] as i32) != 108) { bad = bad + 1; }
        if ((a[0] as i32) != 97) { bad = bad + 2; }
        var r: [u8] = tail(s);
        if (r.len() != 2 || (r[1] as i32) != 108) { bad = bad + 4; }
        var q: [u8] = s.as_bytes()[6:11];
        if (q.len() != 5 || (q[0] as i32) != 119) { bad = bad + 8; }
        i = i + 1;
    }
    return bad + __rc_underflow_count();
}`,
	},
	{
		// A slice header stored in a struct field, an array element and a
		// tuple is retained on the store (needsRcIncOnAlias) and released by
		// the container's own drop — the same counted-alias protocol every
		// other rc-tracked shape runs. The source local's exit dec only decs
		// the shared header; the last container to let go frees it. The
		// `[u8][]` element walk is the per-element __fern_closure_drop of
		// __drop_arr_slice; a bare __fern_rc_dec would strand the fresh
		// second element at rc 0. Closure captures are exercised by the
		// correctness-only view program (closure churn is a pinned gap of
		// its own).
		name: "slice_header_in_containers_churn_free",
		src: `
struct View { v: [u8], n: i32 }
function first(v: View): i32 { return v.v[0] as i32; }
function main(): i32 {
    var s: string = "hello world, this is a heap string";
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 2000) {
        var b: [u8] = s.as_bytes();
        var w: View = View { v: b, n: i };
        var arr: [u8][] = [b, s.as_bytes()];
        var pair: ([u8], i32) = (b, 7);
        var c: [u8] = arr[1];
        if (first(w) != 104) { bad = bad + 1; }
        if (arr[1].len() != s.len() || c.len() != s.len()) { bad = bad + 2; }
        if (pair.0.len() != s.len() || pair.1 != 7) { bad = bad + 4; }
        if (w.n != i) { bad = bad + 16; }
        i = i + 1;
    }
    return bad + __rc_underflow_count();
}`,
	},
	{
		// The `.with` half of the superseded-field move: `x = S { ...x, f:
		// x.f.with(i, v) }` (and the return form, and a method receiver)
		// hands the field to __fern_arr_cow_inplace at the box's own count,
		// so a unique box rewrites its buffer in place and its emptied slot
		// no-ops at the overwrite drop; a shared box retains, and the copy
		// leaves the alias's view intact. Value-checked through an alias, a
		// borrowed caller-side box, and the rebuild loop a streaming hasher's
		// pending block is written as.
		name: "struct_update_field_with_move",
		src: `
struct H { buf: u8[], n: i32, tag: i32 }
function push(own h: H, b: u8): H {
    return H { ...h, buf: h.buf.with(h.n, b), n: h.n + 1 };
}
function (h: H) push_m(b: u8): H {
    h = H { ...h, buf: h.buf.with(h.n, b), n: h.n + 1 };
    return h;
}
function shared(): i32 {
    var h: H = H { buf: __alloc_u8(4), n: 0, tag: 1 };
    h = H { ...h, buf: h.buf.with(0, 5 as u8) };
    var keep: H = h;
    h = H { ...h, buf: h.buf.with(0, 9 as u8), n: 1 };
    var callee_keep: H = keep.push_m(7 as u8);
    return (keep.buf[0] as i32) * 100 + (h.buf[0] as i32) * 10 + (callee_keep.buf[0] as i32) + callee_keep.n * 1000 + keep.n * 10000;
}
function chain(): i32 {
    var h: H = H { buf: __alloc_u8(4), n: 0, tag: 2 };
    h = H { ...h, buf: h.buf.with(0, 1 as u8).with(1, 2 as u8), n: 2 };
    return (h.buf[0] as i32) + (h.buf[1] as i32) * 10;
}
function main(): i32 {
    var h: H = H { buf: __alloc_u8(64), n: 0, tag: 0 };
    var i: i32 = 0;
    while (i < 64) {
        if (i % 2 == 0) { h = push(h, (i * 3) as u8); } else { h = h.push_m((i * 3) as u8); }
        i = i + 1;
    }
    var bad: i32 = 0;
    if (h.n != 64) { bad = bad + 1; }
    if ((h.buf[63] as i32) != 189) { bad = bad + 2; }
    if ((h.buf[10] as i32) != 30) { bad = bad + 4; }
    if (shared() != 1597) { bad = bad + 8; }
    if (chain() != 21) { bad = bad + 16; }
    return bad + __rc_underflow_count();
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
