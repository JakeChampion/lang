package e2eselfhost

import (
	"strings"
	"testing"
)

// --- `let v: T = t` on an rc container (#7282) -------------------------------
//
// A plain alias bind released NOTHING — not the box, not its payload — because
// three things all pointed at the alias at once: the bind emitted no retain
// (the alias-inc was gated on `is_arr_slot`), the source lost its credit to the
// escape gate, and the alias earned none of its own. Four releases lost, not
// one, and `frees=0` rather than a partial count.
//
// THE MODEL IS DUPLICATION, NOT TRANSFER — except at a proven MOVE site. Both
// slots own a counted reference and both release it; the refcount arbitrates.
// `alias_in_a_conditional` is why: under a transfer model `if (c) { let v = t; }`
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
//
// Counts are one block per heap string (#7351 fused the box into the
// buffer's reserved header), and every row balances.

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
			// #7282's repro. `let t: (i32, i32[]) = (i, xs); let v = t;` — the
			// bind now retains the box, the alias carries the source's shallow
			// credit, and both slots sweep. Base: allocs=200 frees=0, 8000.
			name: "tuple_alias",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let v: (i32, i32[]) = t;
    return v.1[0] + v.1[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 200, frees: 200,
		},
		{
			// THE DEEP-RELEASE CASE. `"TUPRCS:"` frees by TYPE — every rc
			// position, then the box — so giving the alias that credit freed the
			// element twice: exit 99, with allocs == frees at live_bytes 0. The
			// alias takes the shallow `"TUP:"` box dec instead. Base 200/0, 8000.
			name: "tuple_alias_fresh_element",
			src: `function round(i: i32): i32 {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    let v: (i32, i32[]) = t;
    return v.1[0] + v.1[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
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
    let t: (i32, i32[]) = (i, [i, i + 1]);
    let v: (i32, i32[]) = t;
    let n: i32 = v.0;
    return n + t.1.len() + i;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 57, allocs: 200, frees: 200,
		},
		{
			// A scalar tuple is not boxed, so there is nothing to retain or release.
			name: "tuple_alias_scalar",
			src: `function round(i: i32): i32 {
    let t: (i32, i32) = (i, i + 1);
    let v: (i32, i32) = t;
    return v.0 + v.1;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 0, frees: 0,
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
    let t: P = P { xs: [i, i + 1] };
    let v: P = t;
    return v.xs[0] + v.xs[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
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
    let t: P = P { xs: [i, i + 1] };
    let v: P = t;
    let n: i32 = v.xs[0];
    return n + t.xs[1] + i;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 10, allocs: 200, frees: 200,
		},
		{
			// The fresh-RET-CALL producer, the other half of the struct credit's
			// collector pair (collect_fresh_ret_call_names). Base: 200/0, 8000.
			name: "struct_alias_fresh_call",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 {
    let t: P = mk(i);
    let v: P = t;
    return v.xs[0] + i;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			// The conditional alias — duplication rather than transfer, so the
			// source is swept on the branch that took no alias. Base: 200/0, 8000.
			name: "struct_alias_in_a_conditional",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 {
    let t: P = mk(i);
    if (i % 2 == 0) { let v: P = t; return v.xs[0] + i; }
    return t.xs[0] + i;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			// THE ROW THAT CARRIES THE MOST WEIGHT. 161 of the 173 struct alias
			// binds in examples/self_host are PARAMETER-origin — 93% — so the
			// CALLEE-side refusal is what this row pins: a parameter is borrowed
			// and owns nothing, and slot_is_reclaimable_struct refuses one at its
			// first line, so `let v: P = p` inside take neither retains nor
			// releases. The CALLER's sweep is the half that moved: the plan does
			// not taint a plain call arg (struct routing wave), so t is swept in
			// round and the cell is clean. The failure guarded against is the
			// callee alias gaining a retain or release while the param owns
			// nothing — that shows up here as an underflow or a count drift.
			name: "struct_alias_of_a_parameter_refused",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function take(p: P): i32 { let v: P = p; return v.xs[0]; }
function round(i: i32): i32 { let t: P = mk(i); return take(t) + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			// A RECEIVER source, a parameter by another spelling: the alias inside the
			// method neither retains nor releases, and the caller's sweep frees `t`.
			name: "struct_alias_of_a_receiver_refused",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
pub function (p: P) first(): i32 { let v: P = p; return v.xs[0]; }
function round(i: i32): i32 { let t: P = mk(i); return t.first() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			// A reassigned alias: the value it held before the rebind and the one after
			// are both freed.
			name: "struct_alias_reassigned_refused",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 { let t: P = mk(i); let v: P = t; v = mk(i + 1); return v.xs[0] + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 400, frees: 400,
		},
		{
			// REFUSED, conservatively: in a chain `let v = t; let u = v;` the middle
			// binding escapes as a bare ident, so it is not an eligible alias site
			// and t keeps no credit either. It leaks rather than over-releasing, and
			// is pinned so widening the alias set later has to face it deliberately.
			name: "struct_alias_chain",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 { let t: P = mk(i); let v: P = t; let u: P = v; return u.xs[0] + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			// The same chain, reading the SOURCE rather than the last link. It
			// used to free exactly HALF (200/100) where the row above freed
			// nothing, which is why it has its own row: the row above cannot tell
			// a partial fix from no fix, and the prescription written here was
			// that a chain widening has to move BOTH to 200/200 while moving
			// either PAST 200 frees is the over-release direction. #7386 does
			// exactly that, and the 99 guard is what says so rather than the byte
			// count.
			name: "struct_alias_chain_source_read",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 { let t: P = mk(i); let v: P = t; let u: P = v; return t.xs[0] + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			// THREE links. The closure is transitive, so a chain does not have a
			// length the rule stops at; two links passing while three leak would
			// mean the walk terminates early rather than that the set is proven.
			name: "struct_alias_chain_three_links",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 { let t: P = mk(i); let v: P = t; let u: P = v; let z: P = u; return z.xs[0] + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			// The chain lives in an IF ARM while the source outlives it. Every
			// link retains on the taken path and releases there, and the source
			// sweeps unconditionally — the branch-dependence the duplication
			// model exists for, one link deeper.
			name: "string_alias_chain_conditional",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let t: string = w("ab"); let n: i32 = 0; if (i % 2 == 0) { let v: string = t; let u: string = v; n = u.len(); } return n + t.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 5, allocs: 100, frees: 100,
		},
		{
			// The LAST link is returned, so the box outlives the frame and the caller
			// frees it.
			name: "string_alias_chain_link_returned_refused",
			src: `function w(a: string): string { return a + "!"; }
function esc(i: i32): string { let t: string = w("ab"); let v: string = t; let u: string = v; return u; }
function round(i: i32): i32 { return esc(i).len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 100, frees: 100,
		},
		{
			// A MIDDLE link is stored into a container that outlives it; the box stays
			// live until `held` is released, and everything balances.
			name: "string_alias_chain_middle_link_held_refused",
			src: `function w(a: string): string { return a + "!"; }
function sink(xs: string[]): i32 { return xs.len(); }
function round(i: i32): i32 { let t: string = w("ab"); let v: string = t; let u: string = v; let held: string[] = [v]; return u.len() + sink(held) + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 38, allocs: 200, frees: 200,
		},
		{
			// The rc-TUPLE CHAIN, credited as one set (#7750). It was the last
			// limb left refused after #7386, because the bare chain credit
			// measured an OVER-RELEASE here that the census cannot see: exit 99
			// at `200/200 live_bytes 0`.
			//
			// The tuple limbs perform move-on-alias credit REWRITE: at a move the
			// deep "TUPRCS:" class migrates from the source to the alias row and
			// the alias's shallow "TUP:" row is dropped. After the first hop `v`
			// therefore holds "TUPRCS:" ALONE, and the ladder's retain gate at the
			// second hop asked only for "TUP:" / "TUPRC:" — so `let u = v` found
			// its source uncredited: no retain, no move-elision of `v`, while the
			// credit pass had already granted `u` its "TUP:" row. FERN_RC_TRACE
			// on one round: two allocs, two frees, NO retain, and the exit sweep
			// dec'd the box from `u` (shallow, freeing it) and again from `v`
			// (deep, reading `.1` out of the freed box first — the sanitizer
			// reports the use-after-free). The gate now asks
			// slot_is_credited_tuple, which names all three states a credited
			// source can be in. Base 200/0, 8000.
			//
			// The element is read through the LAST link on purpose: that is what
			// puts the deep free's box read after the shallow dec in the failing
			// order, so this row is a use-after-free under the sanitize leg and
			// not only an underflow.
			name: "tuple_alias_chain",
			src: `function round(i: i32): i32 { let t: (i32, i32[]) = (i, [i, i + 1]); let v: (i32, i32[]) = t; let u: (i32, i32[]) = v; return u.1.len() + u.0; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			// The chain reading the SOURCE, so the first hop is NOT a move: `t`
			// keeps its deep class, `v` is retained against and takes the
			// shallow row, and the second hop moves `v` into `u`. The struct
			// limb's source-read row is the one that told a half fix from a
			// whole one; this is the tuple pair's.
			name: "tuple_alias_chain_source_read",
			src: `function round(i: i32): i32 { let t: (i32, i32[]) = (i, [i, i + 1]); let v: (i32, i32[]) = t; let u: (i32, i32[]) = v; return t.1.len() + u.0; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			// The chain reading the MIDDLE link after the last bind, so the
			// second hop is a DUPLICATION whose source is a moved-into link: `v`
			// holds "TUPRCS:" alone, and the retain against it is the one the
			// old gate could not fire. `u` takes the shallow dec, `v` the deep
			// free, and the box needs the rc of 2 that retain provides.
			name: "tuple_alias_chain_middle_read",
			src: `function round(i: i32): i32 { let t: (i32, i32[]) = (i, [i, i + 1]); let v: (i32, i32[]) = t; let u: (i32, i32[]) = v; return u.1.len() + v.0; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			// THREE links: the deep class migrates twice, and every hop after
			// the first reads a "TUPRCS:"-only source.
			name: "tuple_alias_chain_three_links",
			src: `function round(i: i32): i32 { let t: (i32, i32[]) = (i, [i, i + 1]); let v: (i32, i32[]) = t; let u: (i32, i32[]) = v; let z: (i32, i32[]) = u; return z.1.len() + z.0; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			// The chain in an IF ARM. Moves are top-level only, so no rewrite
			// happens here: every link retains and takes the shallow dec on the
			// taken path, and the source deep-frees unconditionally.
			name: "tuple_alias_chain_conditional",
			src: `function round(i: i32): i32 { let t: (i32, i32[]) = (i, [i, i + 1]); let n: i32 = 0; if (i % 2 == 0) { let v: (i32, i32[]) = t; let u: (i32, i32[]) = v; n = u.1.len(); } return n + t.1.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 200, frees: 200,
		},
		{
			// The LAST link is returned; the caller frees the box and its element.
			name: "tuple_alias_chain_link_returned_refused",
			src: `function esc(i: i32): (i32, i32[]) { let t: (i32, i32[]) = (i, [i, i + 1]); let v: (i32, i32[]) = t; let u: (i32, i32[]) = v; return u; }
function round(i: i32): i32 { let r: (i32, i32[]) = esc(i); return r.1.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			// A MIDDLE link is stored into a container that outlives it; the box,
			// its element and `held`'s buffer are all freed.
			name: "tuple_alias_chain_middle_link_held_refused",
			src: `function sink(xs: (i32, i32[])[]): i32 { return xs.len(); }
function round(i: i32): i32 { let t: (i32, i32[]) = (i, [i, i + 1]); let v: (i32, i32[]) = t; let u: (i32, i32[]) = v; let held: (i32, i32[])[] = [v]; return u.1.len() + sink(held) + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 300, frees: 300,
		},
		{
			// The SCALAR tuple chain: the tuple is not boxed, so each hop is a copy.
			name: "tuple_alias_scalar_chain",
			src: `function round(i: i32): i32 { let t: (i32, i32) = (i, i + 1); let v: (i32, i32) = t; let u: (i32, i32) = v; return u.0 + u.1; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 0, frees: 0,
		},
		{
			name: "tuple_alias_scalar_chain_middle_read",
			src: `function round(i: i32): i32 { let t: (i32, i32) = (i, i + 1); let v: (i32, i32) = t; let u: (i32, i32) = v; return u.0 + v.1; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 0, frees: 0,
		},
		{
			name: "tuple_alias_scalar_chain_three_links",
			src: `function round(i: i32): i32 { let t: (i32, i32) = (i, i + 1); let v: (i32, i32) = t; let u: (i32, i32) = v; let z: (i32, i32) = u; return z.0 + z.1; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 0, frees: 0,
		},
		{
			name: "tuple_alias_scalar_chain_conditional",
			src: `function round(i: i32): i32 { let t: (i32, i32) = (i, i + 1); let n: i32 = 0; if (i % 2 == 0) { let v: (i32, i32) = t; let u: (i32, i32) = v; n = u.1; } return n + t.0 + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 33, allocs: 0, frees: 0,
		},
		{
			// The last link returned, scalar limb.
			name: "tuple_alias_scalar_chain_link_returned_refused",
			src: `function esc(i: i32): (i32, i32) { let t: (i32, i32) = (i, i + 1); let v: (i32, i32) = t; let u: (i32, i32) = v; return u; }
function round(i: i32): i32 { let r: (i32, i32) = esc(i); return r.0 + r.1; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 0, frees: 0,
		},
		{
			// A middle link held by a container, scalar limb. The allocations are
			// `held`'s buffer.
			name: "tuple_alias_scalar_chain_middle_link_held_refused",
			src: `function sink(xs: (i32, i32)[]): i32 { return xs.len(); }
function round(i: i32): i32 { let t: (i32, i32) = (i, i + 1); let v: (i32, i32) = t; let u: (i32, i32) = v; let held: (i32, i32)[] = [v]; return u.0 + sink(held) + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 200, frees: 200,
		},
		{
			// AN ENUM with an rc payload, aliased. Enum locals carry their enum name in
			// the same struct_type field a type test would read, but take the enum
			// release rather than the struct credit; the shape balances.
			name: "enum_alias_reclaimed",
			src: `enum E { A(i32[]), B }
function mke(i: i32): E { if (i % 2 == 0) { return E.A([i, i + 1]); } return E.B; }
function round(i: i32): i32 { let e: E = mke(i); let f: E = e; let n: i32 = 0; match (f) { E.A(k) => { n = k[0]; }, E.B => { n = 1; } } return n; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 10, allocs: 100, frees: 100,
		},
		{
			// A STRUCT ARRAY, aliased: the slot carries the element type "P" while
			// holding a buffer, and takes the array release, never the struct credit.
			name: "struct_array_alias_unchanged",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 { let ps: P[] = [mk(i)]; let qs: P[] = ps; return qs[0].xs[0] + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 300, frees: 300,
		},
		{
			// A STRUCT-PATTERN `if let`, which is an alias site because its
			// SCRUTINEE desugars to a bare-ident `let` bind of the local. Nothing
			// in the source text looks like `let v = p`, which is why a regex over
			// `let x: T = y;` counted zero creditable sites in conformance while
			// if_let_pattern_forms had two.
			//
			// This row exists because the emit-hash sweep FALSIFIED that
			// prediction. Bisecting the fixture: this shape moves 100/0 -> 100/100
			// (rc_inc 0 -> 1, arr_dec 0 -> 4), and so does the `..` rest form.
			name: "struct_if_let_destructure_alias",
			src: `struct P { x: i32, y: i32 }
function round(i: i32): i32 {
    let total: i32 = 0;
    let p: P = P { x: 3 + i, y: 4 + i };
    if let P { x, y } = p { total = total + x + y; }
    return total;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 59, allocs: 100, frees: 100,
		},
		{
			// The AS-PATTERN form of the same statement. `w @ P { .. }` binds w
			// to the whole scrutinee via build_struct_match's scrutinee cache —
			//     var __sm.._v = p;      <- alias level 1
			//     let w = __sm.._v;      <- alias level 2
			// — an ALIAS CHAIN, which the credit-side escape gate refused
			// conservatively (this row measured 100/0 then). The plan's verdict
			// (struct routing wave) forgives the chain for this SCALAR-ONLY
			// struct and the cell is clean; the rc-FIELD chain is still refused
			// The chain rule it desugars to is credited now (#7386), so this row
			// and struct_alias_chain above stand or fall together.
			name: "struct_as_pattern_binder",
			src: `struct P { x: i32, y: i32 }
function round(i: i32): i32 {
    let total: i32 = 0;
    let p: P = P { x: 3 + i, y: 4 + i };
    if let w @ P { x, y } = p { total = total + w.x + y; }
    return total;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 59, allocs: 100, frees: 100,
		},
		{
			// THE #7368 REGRESSION GUARD. It contains no struct: the INT_MIN input is a
			// scalar slot a type test once mistook for a struct box and retained,
			// dereferencing 0x7FFFFFF8. A type test cannot gate a retain; only the credit can.
			name: "integer_slot_not_retained",
			src: `import "std/i32";
function round(i: i32): i32 {
    let n: i32 = 0 - 2147483647 - 1 + i;
    let s: string = n.to_string();
    return s.len() + i;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 74, allocs: 198, frees: 198,
		},
		{
			// The source read AFTER the alias, so both are live across the
			// bind. This is the row that proved the residual was block scope and
			// not the extra read — it was already clean when the block-scoped
			// shapes were not.
			name: "alias_with_post_read",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let v: (i32, i32[]) = t;
    return v.1[0] + t.1[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
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
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let acc: i32 = 0;
    { let v: (i32, i32[]) = t; acc = acc + v.1[0]; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
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
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let acc: i32 = 0;
    if (i % 2 == 0) { let v: (i32, i32[]) = t; acc = acc + v.1[0]; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 43, allocs: 200, frees: 200,
		},
		{
			// Both factors at once. Base 200/0, 8000.
			name: "conditional_alias_with_post_read",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let acc: i32 = 0;
    if (i % 2 == 0) { let v: (i32, i32[]) = t; acc = acc + v.1[0]; }
    return acc + t.1[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
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
    let t: i32[] = [i, i + 1];
    let v: i32[] = t;
    return v[0] + v[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 100, frees: 100,
		},
		{
			// The array control for block scope. Clean throughout — the array
			// path consults no escape scan, so it could not have warned anyone about
			// the threading bug the tuple rows caught.
			name: "array_alias_in_a_block",
			src: `function round(i: i32): i32 {
    let t: i32[] = [i, i + 1];
    let acc: i32 = 0;
    { let v: i32[] = t; acc = acc + v[0]; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53, allocs: 100, frees: 100,
		},
		{
			// The array control for the conditional. Clean throughout.
			name: "array_alias_in_a_conditional",
			src: `function round(i: i32): i32 {
    let t: i32[] = [i, i + 1];
    let acc: i32 = 0;
    if (i % 2 == 0) { let v: i32[] = t; acc = acc + v[0]; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 43, allocs: 100, frees: 100,
		},
		{
			// The alias is RETURNED, so the caller frees the box and its element.
			name: "refused_alias_escapes",
			src: `function sink(q: (i32, i32[])): i32 { return q.1[0]; }
function mk(i: i32): (i32, i32[]) {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let v: (i32, i32[]) = t;
    return v;
}
function round(i: i32): i32 { let r: (i32, i32[]) = mk(i); return r.1[0]; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53, allocs: 200, frees: 200,
		},
		{
			// The alias is REASSIGNED; the tuple it held and the one it holds after the
			// rebind are both freed.
			name: "refused_alias_reassigned",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let v: (i32, i32[]) = t;
    v = (i + 1, xs);
    return v.1[0];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53, allocs: 300, frees: 300,
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
    let t: string = w("ab");
    let v: string = t;
    return v.len() + i;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 100, frees: 100,
		},
		{
			// The CANCELLED path (#4402 opt 1, string limb): the alias is read
			// but never returned and the source is read after it, so the
			// inc/dec pair is elided — the counts cannot move (a paired
			// cancellation changes rc traffic, not allocs/frees), and the
			// __rc_underflow_count guard is what catches an UNpaired elision
			// (inc skipped while the sweep dec still fires).
			name: "string_alias_cancelled",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: string = w("ab");
    let v: string = t;
    let n: i32 = v.len();
    return n + t.len() + i;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 72, allocs: 100, frees: 100,
		},
		{
			// A PARAMETER is borrowed, never owned, so aliasing one may not RETAIN:
			// the retain is gated on slot_is_reclaimable_str, whose first line
			// refuses a parameter, and the credit is only ever copied from a source
			// that already held one. That is unchanged and is what this row still
			// guards. An unconditional `is_str` in the retain once gave
			// `let sp: string = sep;` inside std/array's join_with_last an inc
			// nothing gives back; an unbalanced retain allocates nothing and frees
			// nothing, so it is invisible on its own and shows up HERE, as the
			// CALLER's box never reaching 0.
			//
			// wantFrees moved 0 -> 200 with the aliased-param borrow verdict, and
			// the guard is sharper for it, not weaker: the caller's box now DOES
			// reach 0, so an unbalanced retain shows as this count falling BELOW
			// allocs rather than as a leak that was already there for another
			// reason. `plen` aliases its param into `v`, reads `v.len()` and returns
			// an i32 — `v` never escapes, so `p` is a borrow and `t` keeps its own
			// credit rather than being treated as escaping into the call.
			//
			// Checked rather than assumed, because 0 -> 200 is also the direction an
			// over-release moves in: the answer is unchanged at 21,
			// __rc_underflow_count() is 0, -sanitize reports neither a
			// use-after-free nor a double free, and the settling form has the CALLER
			// read `t` back after the call with two fresh strings allocated in
			// between — it returns native's answer with allocs == frees.
			name: "string_alias_of_a_parameter_borrowed",
			src: `function w(a: string): string { return a + "!"; }
function plen(p: string): i32 { let v: string = p; return v.len(); }
function round(i: i32): i32 { let t: string = w("ab"); return plen(t) + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 100, frees: 100,
		},
		{
			// The conditional alias, first-class, and the reason the model is
			// DUPLICATION rather than transfer: under a transfer model the source is
			// left un-swept on the branch where no transfer happened, so the leak
			// becomes branch-dependent. Base: allocs=200 frees=0, 3200 live.
			name: "string_alias_in_a_conditional",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: string = w("ab");
    if (i % 2 == 0) { let v: string = t; return v.len() + i; }
    return t.len() + i;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 100, frees: 100,
		},
		{
			// `<scalar>.to_string()`: the "STR:" class has TEN producer families and each
			// grants the credit at its own site, so these rows walk the families that
			// allocate observably.
			name: "string_alias_to_string_producer",
			src: `import "std/i32";
function round(i: i32): i32 { let t: string = i.to_string(); let v: string = t; return v.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 77, allocs: 200, frees: 200,
		},
		{
			// `xs.join(sep)`. Base: allocs=700 frees=500, 3200 live.
			//
			// Two per round, not four: `__fern_arr_str_join` sizes one exact
			// buffer and memcpys into it, where the `r = r + xs[i]` form it
			// replaced allocated a fresh result per element (three for this
			// two-element join, plus the array box). Frees track allocs and
			// live_bytes stays 0, so the forgiveness still reaches the shape —
			// there is simply less to forgive. `ids` keeps the array a heap box.
			name: "string_alias_join_producer",
			src: `import "std/array";
function ids(s: string): string { return s; }
function round(i: i32): i32 { let xs: string[] = [ids("ab"), "cd"]; let t: string = xs.join(","); let v: string = t; return v.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 55, allocs: 200, frees: 200,
		},
		{
			// `<string>.replace(old, new)`. Base: allocs=400 frees=200, 3200 live.
			name: "string_alias_replace_producer",
			src: `import "std/string";
function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let s: string = w("aXb"); let t: string = s.replace("X", "Y"); let v: string = t; return v.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 38, allocs: 200, frees: 200,
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
function round(i: i32): i32 { let s: string = w("  ab  "); let t: str = s.trim(); let v: str = t; return v.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 55, allocs: 200, frees: 200,
		},
		{
			// REFUSED, and correctly so: a chain `let v = t; let u = v;` makes v itself
			// escape as a bare ident, so v is not an eligible alias site and t keeps no
			// credit either. Conservative — it leaks rather than over-releasing — and
			// pinned so that widening the alias set later has to face this case
			// deliberately instead of discovering it as a double free.
			name: "string_alias_chain",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let t: string = w("ab"); let v: string = t; let u: string = v; return u.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 100, frees: 100,
		},
		{
			// A REASSIGNED alias: both strings it held are freed.
			name: "string_alias_reassigned_refused",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let t: string = w("ab"); let v: string = t; v = w("cd"); return v.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 200, frees: 200,
		},
		{
			// A string-builder ACCUMULATOR aliased after its last rebind. The first
			// append onto the empty literal allocates the box and the other two grow
			// it in place (#10960), so the alias shares the one box the round made.
			name: "string_accumulator_alias_refused",
			src: `function round(i: i32): i32 { let s: string = ""; let k: i32 = 0; while (k < 3) { s = s + "x"; k = k + 1; } let v: string = s; return v.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 100, frees: 100,
		},
		{
			// A FOR-IN ELEMENT source, borrowed from the array rather than owned.
			name: "string_alias_of_a_for_in_element",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let xs: string[] = [w("ab")]; let n: i32 = 0; for e in xs { let v: string = e; n = n + v.len(); } return n + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 200, frees: 200,
		},
		{
			// A TUPLE-DESTRUCTURE BINDER source (`let (a, b) = mk(); let v = a;`), the
			// shape parser.fern uses four times.
			name: "string_alias_of_a_destructure_binder",
			src: `function w(a: string): string { return a + "!"; }
function mk(): (string, i32) { return (w("ab"), 7); }
function round(i: i32): i32 { let (a, b) = mk(); let v: string = a; return v.len() + b + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 57, allocs: 200, frees: 200,
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
    let src: string[] = [mkstr("x"), mkstr("y")];
    let t: i32 = 0;
    let x: string[] = src;
    t = (t + x.len()) % 101;
    t = (t + src.len()) % 101;
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68, allocs: 300, frees: 300,
		},
		{
			// The block-scoped alias site — the matrix's if_block row, first-class.
			name: "strarr_alias_in_a_conditional",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let src: string[] = [mkstr("x"), mkstr("y")];
    let t: i32 = 0;
    if (i % 2 == 0) { let x: string[] = src; t = (t + x.len()) % 101; }
    t = (t + src.len()) % 101;
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 51, allocs: 300, frees: 300,
		},
		{
			// ELEMENT BYTES read through both slots before the sweep — the answer
			// is what proves the gated walk freed elements exactly once and late:
			// a premature element free turns 'x'/'y' into recycled bytes (a wrong
			// answer, the #7393 signature), a double walk trips the underflow 99.
			name: "strarr_alias_element_bytes",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let src: string[] = [mkstr("x"), mkstr("y")];
    let x: string[] = src;
    return (x[0][0] as i32 + src[1][0] as i32 + i) % 101;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 32, allocs: 300, frees: 300,
		},
		{
			// The CHAIN, credited as one set (#7750). It used to be refused —
			// `let y = x` is a bare-ident bind the per-site forgiveness list
			// could not hold, so x was strarr-unsafe and src kept no credit,
			// leaving the element box and data to leak while the shallow is_arr
			// decs still returned the buffer.
			//
			// strarr_alias_chain_sites_of walks the closure raw and vets the set
			// through the strarr gate, which is what an element escape from ANY
			// link has to be caught by — strarr_alias_chain_elem_escape_refused
			// below is that row.
			name: "strarr_alias_chain",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let src: string[] = [mkstr("x")];
    let x: string[] = src;
    let y: string[] = x;
    return (x.len() + y.len() + src.len() + i) % 101;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68, allocs: 200, frees: 200,
		},
		{
			// An ELEMENT escapes from a MIDDLE link. A string[]'s release walks the
			// elements, so a free that ignored `e` would leave it dangling.
			name: "strarr_alias_chain_elem_escape_refused",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let src: string[] = [mkstr("x")];
    let x: string[] = src;
    let y: string[] = x;
    let e: string = x[0];
    return (y.len() + e.len() + i) % 101;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68, allocs: 200, frees: 200,
		},
		{
			// The rc-ENUM chain (#7750). Its limb uses the alias sites for ESCAPE
			// FORGIVENESS only — a confined link takes no release, so the source
			// stays the sole releaser and the box is freed once however long the
			// chain is. That is what makes this widening cheap: no link gains a
			// dec, so the arithmetic the string limb has to reason about does not
			// arise here.
			name: "enum_alias_chain",
			src: `enum E { A(i32[]), B }
function round(i: i32): i32 {
    let t: E = E.A([i, i + 1]);
    let v: E = t;
    let u: E = v;
    match (u) { E.A(a) => { return a.len() + i; }, E.B => { return i; } }
    return 0;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			// A link hands the PAYLOAD out. The enum release deep-drops it, so freeing
			// it while `out` holds it would be a use-after-free.
			name: "enum_alias_chain_payload_out_refused",
			src: `enum E { A(i32[]), B }
function round(i: i32): i32 {
    let t: E = E.A([i, i + 1]);
    let v: E = t;
    let u: E = v;
    let out: i32[] = [0];
    match (u) { E.A(a) => { out = a; }, E.B => {} }
    return out.len() + i;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			// An ELEMENT BIND from the alias (`let e = x[0]`) is a lasting element
			// pointer; the deep free must not run while `e` holds it.
			name: "strarr_alias_elem_bind_refused",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let src: string[] = [mkstr("x"), mkstr("y")];
    let x: string[] = src;
    let e: string = x[0];
    return (e.len() + src.len() + i) % 101;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 67, allocs: 300, frees: 300,
		},
		{
			// The string[] sibling of the row above, and it moved with it: a
			// parameter source still owns nothing to share and is still never
			// listed by the collector, but `plen` only reads its alias, so the
			// param is a BORROW and the CALLER's `src` keeps its credit — element
			// included, which is the half that used to leak.
			//
			// wantFrees moved 100 -> 300. Same checks as the row above: answer
			// unchanged at 70, underflow 0, sanitizer clean, and the churn form
			// (caller reads src and both its elements back after two fresh arrays)
			// returns native's answer with allocs == frees.
			name: "strarr_alias_of_a_parameter_borrowed",
			src: `function mkstr(a: string): string { return a + "!"; }
function plen(p: string[]): i32 { let v: string[] = p; return v.len(); }
function round(i: i32): i32 { let src: string[] = [mkstr("x")]; return (plen(src) + i) % 101; }
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 70, allocs: 200, frees: 200,
		}}
}

// TestSelfHostContainerAliasBindX86_64 — a plain alias of an rc container shares
// its credit, and every row frees what it allocates.
func TestSelfHostContainerAliasBindX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range containerAliasCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := cli.emit(t, "x86-64-linux", tc.src, "FERN_LEAKCHECK=1")
			stderr, exit := hevRun(t, cli.runner, buildBin(t, cli.gcc, t.TempDir(), "alias", asm))
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
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d. FEWER means the alias forgiveness "+
					"stopped reaching this shape (a partial thread shows up as a "+
					"scope-dependent result)", tc.name, summary, tc.frees)
			}

			// Every over-release in this family balances the census, and the
			// underflow counter only sees the SECOND dec of a box — a deep free
			// that reads through a box the shallow dec already returned is a
			// use-after-free the counter reports late or not at all (the tuple
			// chain, #7750). The quarantining allocator reports both directly.
			sanAsm := cli.emit(t, "x86-64-linux", tc.src, "FERN_SANITIZE=1")
			sanErr, sanExit := hevRun(t, cli.runner, buildBin(t, cli.gcc, t.TempDir(), "alias_san", sanAsm))
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}
}

// TestSelfHostContainerAliasBindWasmIR — the wasm sibling. Exit codes only,
// which is the whole signal for the two deep-release rows: an over-release moves
// no byte count on any backend.
func TestSelfHostContainerAliasBindWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range containerAliasCases() {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); got != tc.want {
				t.Errorf("container alias-bind wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostContainerAliasBindIRArm64 — the arm64 sibling under qemu.
func TestSelfHostContainerAliasBindIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range containerAliasCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
