package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- `var v: T = t` on an rc container (#7282) -------------------------------
//
// A plain alias bind released NOTHING — not the box, not its payload — because
// three things all pointed at the alias at once: the bind emitted no retain
// (the alias-inc was gated on `is_arr_slot`), the source lost its credit to the
// escape gate, and the alias earned none of its own. Four releases lost, not
// one, and `frees=0` rather than a partial count.
//
// THE MODEL IS DUPLICATION, NOT TRANSFER — except at a proven MOVE site. Both
// slots own a counted reference and both release it; the refcount arbitrates.
// `alias_in_a_conditional` is why: under a transfer model `if (c) { var v = t; }`
// leaves the source un-swept on the path where no transfer happened, so a leak
// becomes branch-dependent — strictly worse than the leak it replaces.
// Duplication emits the inc and the dec on the same path by construction.
// A TOP-LEVEL alias at the source's last mention is the safe exception: it
// always executes, so the retain and the source's release are elided as one
// decision (moves_local_at + note_moved_elided) — the counts here are
// unchanged by that, since the single box still frees exactly once.
//
// THE INVARIANT: only the BOX is retained at the bind, so only the BOX may be
// released twice. The alias therefore takes the box-only release and the source
// keeps the deep one — `"NODEEP:"` for a struct (a field walk plus a box dec)
// and the shallow `"TUP:"` for an rc-tuple (whose `"TUPRCS:"` release is a
// type-driven deep free). Both deep classes were measured double-freeing at
// exit 99 before that split, with `allocs == frees` at `live_bytes == 0` — the
// census silent, as it is for every over-release.
//
// The ARRAY rows are the reference implementation and must stay byte-neutral:
// arrays already retained at the bind, and their exit sweep is driven by the
// `is_arr` slot FLAG rather than a credit an escape scan can deny, which is why
// they never had the bug — and why they could not have warned anyone about the
// threading defect the block-scoped rows caught.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run.

type containerAliasCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

func containerAliasCases() []containerAliasCase {
	return []containerAliasCase{
		{
			// #7282's repro. `var t: (i32, i32[]) = (i, xs); var v = t;` — the
			// bind now retains the box, the alias carries the source's shallow
			// credit, and both slots sweep. Base: allocs=200 frees=0, 8000.
			name: "tuple_alias",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var v: (i32, i32[]) = t;
    return v.1[0] + v.1[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 200, frees: 200,
		},
		{
			// THE DEEP-RELEASE CASE. `"TUPRCS:"` frees by TYPE — every rc
			// position, then the box — so giving the alias that credit freed the
			// element twice: exit 99, with allocs == frees at live_bytes 0. The
			// alias takes the shallow `"TUP:"` box dec instead. Base 200/0, 8000.
			name: "tuple_alias_fresh_element",
			src: `function round(i: i32): i32 {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var v: (i32, i32[]) = t;
    return v.1[0] + v.1[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 200, frees: 200,
		},
		{
			// The CANCELLED path (#4402 opt 1, tuple limb): the alias is read
			// but never returned and the source is read after it — a LIVE
			// source. The inc and the alias's shallow "TUP:" box dec are
			// elided; the source keeps its deep release. Counts cannot move;
			// the __rc_underflow_count guard catches an unpaired elision.
			name: "tuple_alias_cancelled",
			src: `function round(i: i32): i32 {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var v: (i32, i32[]) = t;
    var n: i32 = v.0;
    return n + t.1.len() + i;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 57, allocs: 200, frees: 200,
		},
		{
			// A box-only tuple: no element release either side. Base 100/0, 4000.
			name: "tuple_alias_scalar",
			src: `function round(i: i32): i32 {
    var t: (i32, i32) = (i, i + 1);
    var v: (i32, i32) = t;
    return v.0 + v.1;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 100, frees: 100,
		},
		{
			// The struct limb, and the one class whose release is NOT the box dec:
			// a struct is a DEEP FIELD DROP (__struct_drop_P) plus a box dec.
			// Under duplication only the box is retained at the bind, so the
			// alias carries "NODEEP:" (box-only) while the source keeps the
			// single field walk; two deep drops would free `xs` twice —
			// measured as exit 99, with allocs == frees at live_bytes 0.
			// This shape is a MOVE (t's last mention is the bind), so the
			// retain is elided and the alias inherits the source's whole
			// release role: no rc_inc, one __struct_drop_P on the alias.
			// Either model frees each allocation exactly once — the counts
			// below hold for both. Base: allocs=200 frees=0, 8000 live.
			name: "struct_alias",
			src: `struct P { xs: i32[] }
function round(i: i32): i32 {
    var t: P = P { xs: [i, i + 1] };
    var v: P = t;
    return v.xs[0] + v.xs[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 200, frees: 200,
		},
		{
			// The CANCELLED path (#4402 opt 1, struct limb): the alias is read
			// but never returned and the source is read after it — a LIVE
			// source, so this is the cancellation, not the move above. The
			// inc and the alias's box-only "NODEEP:" dec are elided; the
			// source keeps the one deep field walk. Counts cannot move (a
			// paired cancellation changes rc traffic, not allocs/frees); the
			// __rc_underflow_count guard catches an unpaired elision.
			name: "struct_alias_cancelled",
			src: `struct P { xs: i32[] }
function round(i: i32): i32 {
    var t: P = P { xs: [i, i + 1] };
    var v: P = t;
    var n: i32 = v.xs[0];
    return n + t.xs[1] + i;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 10, allocs: 200, frees: 200,
		},
		{
			// The fresh-RET-CALL producer, the other half of the struct credit's
			// collector pair (collect_fresh_ret_call_names). Base: 200/0, 8000.
			name: "struct_alias_fresh_call",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 {
    var t: P = mk(i);
    var v: P = t;
    return v.xs[0] + i;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			// The conditional alias — duplication rather than transfer, so the
			// source is swept on the branch that took no alias. Base: 200/0, 8000.
			name: "struct_alias_in_a_conditional",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 {
    var t: P = mk(i);
    if (i % 2 == 0) { var v: P = t; return v.xs[0] + i; }
    return t.xs[0] + i;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			// THE ROW THAT CARRIES THE MOST WEIGHT. 161 of the 173 struct alias
			// binds in examples/self_host are PARAMETER-origin — 93% — so the
			// refusal, not the credit, is what this change mostly does. A parameter
			// is borrowed and owns nothing, and slot_is_reclaimable_struct refuses
			// one at its first line. frees=0 is the status quo (t escapes into the
			// call); the failure guarded against is that count staying 0 while the
			// caller's box stops reaching zero.
			name: "struct_alias_of_a_parameter_refused",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function take(p: P): i32 { var v: P = p; return v.xs[0]; }
function round(i: i32): i32 { var t: P = mk(i); return take(t) + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 0,
		},
		{
			// A RECEIVER source, which is a parameter by another spelling and is
			// refused by the same first line. Distinct row because the origin axis
			// (#7253) names it separately and the builder-threaded
			// `s = s.method(..)` shape is the dominant one in this compiler.
			name: "struct_alias_of_a_receiver_refused",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
pub function (p: P) first(): i32 { var v: P = p; return v.xs[0]; }
function round(i: i32): i32 { var t: P = mk(i); return t.first() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 100,
		},
		{
			// REFUSED: a reassigned alias does not hold the box the credit
			// describes at exit (alias_bind_sites_of's body_assign_targets check).
			name: "struct_alias_reassigned_refused",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 { var t: P = mk(i); var v: P = t; v = mk(i + 1); return v.xs[0] + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 400, frees: 0,
		},
		{
			// REFUSED, conservatively: in a chain `var v = t; var u = v;` the middle
			// binding escapes as a bare ident, so it is not an eligible alias site
			// and t keeps no credit either. It leaks rather than over-releasing, and
			// is pinned so widening the alias set later has to face it deliberately.
			name: "struct_alias_chain_refused",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 { var t: P = mk(i); var v: P = t; var u: P = v; return u.xs[0] + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 0,
		},
		{
			// AN ENUM with an rc payload, aliased. Unchanged, and this is the row
			// that pins the #7368 discrimination from the credit side: enum locals
			// carry their enum NAME in the same struct_type field a type test would
			// have read, but they earn "RCENUM:" / "SCENUMS:" rather than the struct
			// credit, so slot_is_reclaimable_struct refuses them.
			name: "enum_alias_unchanged",
			src: `enum E { A(i32[]), B }
function mke(i: i32): E { if (i % 2 == 0) { return E.A([i, i + 1]); } return E.B; }
function round(i: i32): i32 { var e: E = mke(i); var f: E = e; var n: i32 = 0; match (f) { E.A(k) => { n = k[0]; }, E.B => { n = 1; } } return n; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 10, allocs: 150, frees: 0,
		},
		{
			// A STRUCT ARRAY, aliased. Unchanged, and the second half of the same
			// discrimination: mark_struct_type doubles as the ELEMENT-type slot for
			// struct and enum arrays, so this slot carries "P" while holding a
			// BUFFER. It earns "ARRSTRUCT:", never the bare struct credit.
			name: "struct_array_alias_unchanged",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 { var ps: P[] = [mk(i)]; var qs: P[] = ps; return qs[0].xs[0] + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 300, frees: 100,
		},
		{
			// A STRUCT-PATTERN `if let`, which is an alias site because its
			// SCRUTINEE desugars to a bare-ident `var` bind of the local. Nothing
			// in the source text looks like `var v = p`, which is why a regex over
			// `var x: T = y;` counted zero creditable sites in conformance while
			// if_let_pattern_forms had two.
			//
			// This row exists because the emit-hash sweep FALSIFIED that
			// prediction. Bisecting the fixture: this shape moves 100/0 -> 100/100
			// (rc_inc 0 -> 1, arr_dec 0 -> 4), and so does the `..` rest form.
			name: "struct_if_let_destructure_alias",
			src: `struct P { x: i32, y: i32 }
function round(i: i32): i32 {
    var total: i32 = 0;
    var p: P = P { x: 3 + i, y: 4 + i };
    if let P { x, y } = p { total = total + x + y; }
    return total;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 59, allocs: 100, frees: 100,
		},
		{
			// The AS-PATTERN form of the same statement. It STILL LEAKS, and the
			// name says so: this is an unfixed alias site, not a correct refusal.
			//
			// `w @ P { .. }` binds w to the whole scrutinee, so by the rule that
			// governs every other row here it IS an alias site. It is not credited
			// because build_struct_match caches the scrutinee first —
			//     var __sm.._v = p;      <- alias level 1
			//     var w = __sm.._v;      <- alias level 2
			// — which makes it the second link of an ALIAS CHAIN, and chains are
			// conservatively refused (struct_alias_chain_refused, same numbers).
			// The middle binding escapes as a bare ident, so p loses its credit
			// too: that is why adding `@` to a plain destructure SUPPRESSES the
			// plain one's fix rather than merely failing to add its own.
			//
			// Measured against hand-written analogues, which match exactly:
			//   one level  (`var t = p`)              100/0 -> 100/100
			//   two levels (`var t = p; var w = t;`)  100/0 -> 100/0
			// and `w` bound but NEVER USED is also 100/0, so it is the binding
			// that costs the credit, not any use of w.
			//
			// Fixing the alias chain fixes this shape for free — they are one
			// limitation, not two.
			name: "struct_as_pattern_binder_leaks",
			src: `struct P { x: i32, y: i32 }
function round(i: i32): i32 {
    var total: i32 = 0;
    var p: P = P { x: 3 + i, y: 4 + i };
    if let w @ P { x, y } = p { total = total + w.x + y; }
    return total;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 59, allocs: 100, frees: 0,
		},
		{
			// THE #7368 REGRESSION GUARD, and it is verified to fire rather than
			// assumed to: recompiled under the reverted `struct_type_of_slot` clause
			// this program SEGFAULTS (exit 139), where main and this change both
			// return 74.
			//
			// It contains no struct at all. __fern_i32_to_string binds the negated
			// input, and that scalar slot carries a struct_type name — so on
			// INT_MIN the slot holds 0x80000000, which is non-zero, even, and above
			// 0x10000, passing every __fern_rc_inc guard before dereferencing
			// 0x7FFFFFF8. A type test cannot gate a retain; only the credit can.
			name: "integer_slot_not_retained",
			src: `import "std/i32";
function round(i: i32): i32 {
    var n: i32 = 0 - 2147483647 - 1 + i;
    var s: string = n.to_string();
    return s.len() + i;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 74, allocs: 200, frees: 200,
		},
		{
			// The source read AFTER the alias, so both are live across the
			// bind. This is the row that proved the residual was block scope and
			// not the extra read — it was already clean when the block-scoped
			// shapes were not.
			name: "alias_with_post_read",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var v: (i32, i32[]) = t;
    return v.1[0] + t.1[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 200, frees: 200,
		},
		{
			// BLOCK SCOPE. A first version threaded the escape forgiveness only
			// through the top-level statement loop, so anything nested fell into the
			// un-forgiving walker: function scope measured 200/200 while this sat at
			// 200/100, two dec sites missing from the emitted asm and nothing else
			// to see. Base 200/0, 8000.
			name: "alias_in_a_plain_block",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var acc: i32 = 0;
    { var v: (i32, i32[]) = t; acc = acc + v.1[0]; }
    return acc;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53, allocs: 200, frees: 200,
		},
		{
			// THE SHAPE THAT DECIDED THE MODEL. Under a TRANSFER model the
			// source is left un-swept on the path where no transfer happened, so a
			// leak becomes branch-dependent. Duplication emits the inc and the dec
			// on the same path by construction, which is why this measures like
			// every other row. Base 200/0, 8000.
			name: "alias_in_a_conditional",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var acc: i32 = 0;
    if (i % 2 == 0) { var v: (i32, i32[]) = t; acc = acc + v.1[0]; }
    return acc;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 43, allocs: 200, frees: 200,
		},
		{
			// Both factors at once. Base 200/0, 8000.
			name: "conditional_alias_with_post_read",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var acc: i32 = 0;
    if (i % 2 == 0) { var v: (i32, i32[]) = t; acc = acc + v.1[0]; }
    return acc + t.1[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 30, allocs: 200, frees: 200,
		},
		{
			// THE REFERENCE IMPLEMENTATION — clean before this change and after.
			// Arrays already retained at the bind and are swept by the `is_arr` slot
			// FLAG rather than by a credit an escape scan can deny, which is why
			// they never had this bug. These three rows pin that the change is
			// byte-neutral for them.
			name: "array_alias_reference",
			src: `function round(i: i32): i32 {
    var t: i32[] = [i, i + 1];
    var v: i32[] = t;
    return v[0] + v[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 100, frees: 100,
		},
		{
			// The array control for block scope. Clean throughout — the array
			// path consults no escape scan, so it could not have warned anyone about
			// the threading bug the tuple rows caught.
			name: "array_alias_in_a_block",
			src: `function round(i: i32): i32 {
    var t: i32[] = [i, i + 1];
    var acc: i32 = 0;
    { var v: i32[] = t; acc = acc + v[0]; }
    return acc;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53, allocs: 100, frees: 100,
		},
		{
			// The array control for the conditional. Clean throughout.
			name: "array_alias_in_a_conditional",
			src: `function round(i: i32): i32 {
    var t: i32[] = [i, i + 1];
    var acc: i32 = 0;
    if (i % 2 == 0) { var v: i32[] = t; acc = acc + v[0]; }
    return acc;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 43, allocs: 100, frees: 100,
		},
		{
			// REFUSED — the alias is RETURNED, so a third reference leaves the
			// frame and nothing downstream accounts for it. Unchanged at 8000.
			name: "refused_alias_escapes",
			src: `function sink(q: (i32, i32[])): i32 { return q.1[0]; }
function mk(i: i32): (i32, i32[]) {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var v: (i32, i32[]) = t;
    return v;
}
function round(i: i32): i32 { var r: (i32, i32[]) = mk(i); return r.1[0]; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53, allocs: 200, frees: 0,
		},
		{
			// REFUSED — the alias is REASSIGNED, so its final value is not the
			// box the credit describes. Unchanged at 12000.
			name: "refused_alias_reassigned",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var v: (i32, i32[]) = t;
    v = (i + 1, xs);
    return v.1[0];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53, allocs: 300, frees: 0,
		},
		{
			// The string limb. A string box is rc-headered on every backend —
			// __fern_str_box writes rc=1 and hands back the pointer PAST it, and
			// __fern_str_free reads that word, decrementing above 1 and freeing only
			// at 1 — so the same duplication the containers use applies unchanged.
			// Base: allocs=200 frees=0, 3200 live.
			//
			// Native allocates 0 here (SSO, #7351) where the self-host allocates 200;
			// that divergence is not this change's.
			name: "string_alias",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var t: string = w("ab");
    var v: string = t;
    return v.len() + i;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 200, frees: 200,
		},
		{
			// THE ROW THAT MUST NOT MOVE. A PARAMETER is borrowed, never owned, so
			// aliasing one may not retain: the retain is gated on
			// slot_is_reclaimable_str, whose first line refuses a parameter, and the
			// credit is only ever copied from a source that already held one.
			//
			// This is not hypothetical. An unconditional `is_str` in the retain gave
			// `var sp: string = sep;` inside std/array's join_with_last an inc nothing
			// gives back, on every program that reaches it. An unbalanced retain
			// allocates nothing and frees nothing, so it is invisible to this census
			// on its own — it shows up HERE, as the CALLER's box never reaching 0.
			// frees=0 is the status quo (t escapes into the call), and the failure
			// this row guards against is the count staying 0 while live_bytes grows.
			// The CANCELLED path (#4402 opt 1, string limb): the alias is read
			// but never returned and the source is read after it, so the
			// inc/dec pair is elided — the counts cannot move (a paired
			// cancellation changes rc traffic, not allocs/frees), and the
			// __rc_underflow_count guard is what catches an UNpaired elision
			// (inc skipped while the sweep dec still fires).
			name: "string_alias_cancelled",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var t: string = w("ab");
    var v: string = t;
    var n: i32 = v.len();
    return n + t.len() + i;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 72, allocs: 200, frees: 200,
		},
		{
			name: "string_alias_of_a_parameter_refused",
			src: `function w(a: string): string { return a + "!"; }
function plen(p: string): i32 { var v: string = p; return v.len(); }
function round(i: i32): i32 { var t: string = w("ab"); return plen(t) + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 200, frees: 0,
		},
		{
			// The conditional alias, first-class, and the reason the model is
			// DUPLICATION rather than transfer: under a transfer model the source is
			// left un-swept on the branch where no transfer happened, so the leak
			// becomes branch-dependent. Base: allocs=200 frees=0, 3200 live.
			name: "string_alias_in_a_conditional",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var t: string = w("ab");
    if (i % 2 == 0) { var v: string = t; return v.len() + i; }
    return t.len() + i;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 200, frees: 200,
		},
		{
			// `<scalar>.to_string()` — the "STR:" class has TEN producer families and
			// each grants the credit at its own site, so wiring one says nothing about
			// the others. These rows walk the families that allocate observably.
			// Base: allocs=200 frees=0, 3200 live.
			name: "string_alias_to_string_producer",
			src: `import "std/i32";
function round(i: i32): i32 { var t: string = i.to_string(); var v: string = t; return v.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 77, allocs: 200, frees: 200,
		},
		{
			// `xs.join(sep)`. Base: allocs=700 frees=500, 3200 live.
			name: "string_alias_join_producer",
			src: `import "std/array";
function round(i: i32): i32 { var xs: string[] = ["ab", "cd"]; var t: string = xs.join(","); var v: string = t; return v.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 55, allocs: 700, frees: 700,
		},
		{
			// `<string>.replace(old, new)`. Base: allocs=400 frees=200, 3200 live.
			name: "string_alias_replace_producer",
			src: `import "std/string";
function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { var s: string = w("aXb"); var t: string = s.replace("X", "Y"); var v: string = t; return v.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 38, allocs: 400, frees: 400,
		},
		{
			// Formerly string_alias_trim_view_partial, the pinned view-class
			// residue (300/250, 1200 live): `.trim()` copies since #7393, so the
			// binding is an ordinary fresh string with the full alias treatment
			// and the class CLOSES — 400/400, live 0 (the extra alloc per round
			// is the trim copy). Underflow 0 is the half that must hold: this is
			// now the row that fails if the copy ever reverts to the view whose
			// escape was #7393's wrong-answer UAF.
			name: "string_alias_trim_closes",
			src: `import "std/string";
function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { var s: string = w("  ab  "); var t: str = s.trim(); var v: str = t; return v.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 55, allocs: 400, frees: 400,
		},
		{
			// REFUSED, and correctly so: a chain `var v = t; var u = v;` makes v itself
			// escape as a bare ident, so v is not an eligible alias site and t keeps no
			// credit either. Conservative — it leaks rather than over-releasing — and
			// pinned so that widening the alias set later has to face this case
			// deliberately instead of discovering it as a double free.
			name: "string_alias_chain_refused",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { var t: string = w("ab"); var v: string = t; var u: string = v; return u.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 200, frees: 0,
		},
		{
			// REFUSED: a REASSIGNED alias does not hold the box the credit describes
			// at exit, so alias_bind_sites_of excludes it (body_assign_targets).
			name: "string_alias_reassigned_refused",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { var t: string = w("ab"); var v: string = t; v = w("cd"); return v.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 400, frees: 0,
		},
		{
			// REFUSED, as a property of the class: a string-builder ACCUMULATOR is
			// reassigned by definition and each rebind frees the box it supersedes, so
			// an alias bound before a rebind would point at freed memory. Every wired
			// family carries `index_of_str(reassigned, …) < 0`, so no route grants one.
			name: "string_accumulator_alias_refused",
			src: `function round(i: i32): i32 { var s: string = ""; var k: i32 = 0; while (k < 3) { s = s + "x"; k = k + 1; } var v: string = s; return v.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 600, frees: 0,
		},
		{
			// A FOR-IN ELEMENT source. Unchanged by this change — a loop element is
			// borrowed from the array rather than being a credited string local, so no
			// family grants it a credit to share. Pinned as an origin the axis names
			// and this change does not reach.
			name: "string_alias_of_a_for_in_element",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { var xs: string[] = [w("ab")]; var n: i32 = 0; for e in xs { var v: string = e; n = n + v.len(); } return n + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 300, frees: 100,
		},
		{
			// A TUPLE-DESTRUCTURE BINDER source (`var (a, b) = mk(); var v = a;`) —
			// the origin the #7253 axis gained when it was applied to this shape, and
			// the one no earlier corpus count could have surfaced. Four instances live
			// in the compiler's own parser.fern. Unchanged here: a destructure binder
			// is not a credited string local either.
			name: "string_alias_of_a_destructure_binder",
			src: `function w(a: string): string { return a + "!"; }
function mk(): (string, i32) { return (w("ab"), 7); }
function round(i: i32): i32 { var (a, b) = mk(); var v: string = a; return v.len() + b + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 57, allocs: 300, frees: 0,
		},
		{
			// The string[] limb (#7391), and the one whose alias takes the SAME
			// DEEP "SARR:" class the source holds — where a struct alias must take
			// box-only "NODEEP:". The difference is in the release itself:
			// __fern_str_arr_free is rc-gated (rc>1 decs and leaves the elements
			// to the other owner; only rc==1 walks them), so two credited slots
			// cannot walk twice. #7391 was filed proposing a deep retain against
			// an ungated walk; the gate has been there since #7292, which is what
			// makes the ordinary shallow duplication sound. Base: 500 allocs /
			// 100 frees per 100 rounds, 6400 live.
			name: "strarr_alias",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var src: string[] = [mkstr("x"), mkstr("y")];
    var t: i32 = 0;
    var x: string[] = src;
    t = (t + x.len()) % 101;
    t = (t + src.len()) % 101;
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68, allocs: 500, frees: 500,
		},
		{
			// The block-scoped alias site — the matrix's if_block row, first-class.
			name: "strarr_alias_in_a_conditional",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var src: string[] = [mkstr("x"), mkstr("y")];
    var t: i32 = 0;
    if (i % 2 == 0) { var x: string[] = src; t = (t + x.len()) % 101; }
    t = (t + src.len()) % 101;
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 51, allocs: 500, frees: 500,
		},
		{
			// ELEMENT BYTES read through both slots before the sweep — the answer
			// is what proves the gated walk freed elements exactly once and late:
			// a premature element free turns 'x'/'y' into recycled bytes (a wrong
			// answer, the #7393 signature), a double walk trips the underflow 99.
			name: "strarr_alias_element_bytes",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var src: string[] = [mkstr("x"), mkstr("y")];
    var x: string[] = src;
    return (x[0][0] as i32 + src[1][0] as i32 + i) % 101;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 32, allocs: 500, frees: 500,
		},
		{
			// REFUSED: a chain makes x itself strarr-unsafe (`var y = x` is a
			// bare-ident bind the forgiveness list doesn't hold), so x is not an
			// eligible alias site and src keeps no credit. The shallow is_arr
			// decs still return the BUFFER; the element box + data leak, which is
			// the sound direction.
			name: "strarr_alias_chain_refused",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var src: string[] = [mkstr("x")];
    var x: string[] = src;
    var y: string[] = x;
    return (x.len() + y.len() + src.len() + i) % 101;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68, allocs: 300, frees: 100,
		},
		{
			// REFUSED: an ELEMENT BIND from the alias (`var e = x[0]`) is a
			// lasting element pointer the deep free would dangle — exactly the
			// hazard that makes the alias vet through the strarr gate rather
			// than body_unsafe_for. Sound leak; MORE frees here means the vet
			// weakened.
			name: "strarr_alias_elem_bind_refused",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var src: string[] = [mkstr("x"), mkstr("y")];
    var x: string[] = src;
    var e: string = x[0];
    return (e.len() + src.len() + i) % 101;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 67, allocs: 500, frees: 100,
		},
		{
			// REFUSED: a parameter source owns nothing to share — the collector
			// never lists one, so neither forgiveness nor credit exists. The
			// shallow decs return the buffer; the element leaks. The string[]
			// sibling of string_alias_of_a_parameter_refused.
			name: "strarr_alias_of_a_parameter_refused",
			src: `function mkstr(a: string): string { return a + "!"; }
function plen(p: string[]): i32 { var v: string[] = p; return v.len(); }
function round(i: i32): i32 { var src: string[] = [mkstr("x")]; return (plen(src) + i) % 101; }
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 70, allocs: 300, frees: 100,
		}}
}

// TestSelfHostContainerAliasBindX86_64 — a plain alias of an rc container shares
// its credit, and the shapes that must stay refused still are.
func TestSelfHostContainerAliasBindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range containerAliasCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "alias_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the alias took a "+
					"DEEP release it did not earn — only the box is retained at the bind)",
					tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d. FEWER means the alias forgiveness "+
					"stopped reaching this shape (a partial thread shows up as a "+
					"scope-dependent result); MORE on a refused row means it reached "+
					"one it must decline", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostContainerAliasBindWasmIR — the wasm sibling. Exit codes only,
// which is the whole signal for the two deep-release rows: an over-release moves
// no byte count on any backend.
func TestSelfHostContainerAliasBindWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping container alias-bind wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range containerAliasCases() {
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
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "alias_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("container alias-bind wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostContainerAliasBindIRArm64 — the arm64 sibling under qemu.
func TestSelfHostContainerAliasBindIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range containerAliasCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "alias_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
