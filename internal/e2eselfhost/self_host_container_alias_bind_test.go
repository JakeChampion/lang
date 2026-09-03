package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
//
// Counts here are ONE block per heap string: #7351 fused the box into the
// buffer's reserved header. Every row was re-measured against the commit
// before it, and every live_bytes is unchanged — the clean rows stayed clean
// and each refusal-leak row leaks the same bytes — so what moved is block
// volume, not behaviour. A pre-fusion number in a row note below is the older
// one.

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
			// CALLEE-side refusal is what this row pins: a parameter is borrowed
			// and owns nothing, and slot_is_reclaimable_struct refuses one at its
			// first line, so `var v: P = p` inside take neither retains nor
			// releases. The CALLER's sweep is the half that moved: the plan does
			// not taint a plain call arg (struct routing wave), so t is swept in
			// round and the cell is clean. The failure guarded against is the
			// callee alias gaining a retain or release while the param owns
			// nothing — that shows up here as an underflow or a count drift.
			name: "struct_alias_of_a_parameter_refused",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function take(p: P): i32 { var v: P = p; return v.xs[0]; }
function round(i: i32): i32 { var t: P = mk(i); return take(t) + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
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
			name: "struct_alias_chain",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 { var t: P = mk(i); var v: P = t; var u: P = v; return u.xs[0] + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
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
function round(i: i32): i32 { var t: P = mk(i); var v: P = t; var u: P = v; return t.xs[0] + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			// THREE links. The closure is transitive, so a chain does not have a
			// length the rule stops at; two links passing while three leak would
			// mean the walk terminates early rather than that the set is proven.
			name: "struct_alias_chain_three_links",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { return P { xs: [i, i + 1] }; }
function round(i: i32): i32 { var t: P = mk(i); var v: P = t; var u: P = v; var z: P = u; return z.xs[0] + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 23, allocs: 200, frees: 200,
		},
		{
			// The chain lives in an IF ARM while the source outlives it. Every
			// link retains on the taken path and releases there, and the source
			// sweeps unconditionally — the branch-dependence the duplication
			// model exists for, one link deeper.
			name: "string_alias_chain_conditional",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { var t: string = w("ab"); var n: i32 = 0; if (i % 2 == 0) { var v: string = t; var u: string = v; n = u.len(); } return n + t.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 5, allocs: 100, frees: 100,
		},
		{
			// REFUSED: the LAST link is returned, so the box outlives the frame.
			// All-or-nothing is what this row is for — the escape is on `u`, and
			// it has to cost `t` and `v` their credit too, because all three name
			// the box the caller now holds.
			name: "string_alias_chain_link_returned_refused",
			src: `function w(a: string): string { return a + "!"; }
function esc(i: i32): string { var t: string = w("ab"); var v: string = t; var u: string = v; return u; }
function round(i: i32): i32 { return esc(i).len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 100, frees: 0,
		},
		{
			// REFUSED: a MIDDLE link is stored into a container that outlives it,
			// so the escape is not at either end of the chain. Freeing the box at
			// the ends would strand `held`'s element pointer.
			name: "string_alias_chain_middle_link_held_refused",
			src: `function w(a: string): string { return a + "!"; }
function sink(xs: string[]): i32 { return xs.len(); }
function round(i: i32): i32 { var t: string = w("ab"); var v: string = t; var u: string = v; var held: string[] = [v]; return u.len() + sink(held) + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 38, allocs: 200, frees: 100,
		},
		{
			// The rc-TUPLE CHAIN, credited as one set (#7750). It was the last
			// limb left refused after #7386, because the bare chain credit
			// measured an OVER-RELEASE here that the census cannot see: exit 99
			// at `200/200 live_bytes 0`.
			//
			// The tuple limbs perform move-on-alias credit SURGERY: at a move the
			// deep "TUPRCS:" class migrates from the source to the alias row and
			// the alias's shallow "TUP:" row is dropped. After the first hop `v`
			// therefore holds "TUPRCS:" ALONE, and the ladder's retain gate at the
			// second hop asked only for "TUP:" / "TUPRC:" — so `var u = v` found
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
			src: `function round(i: i32): i32 { var t: (i32, i32[]) = (i, [i, i + 1]); var v: (i32, i32[]) = t; var u: (i32, i32[]) = v; return u.1.len() + u.0; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			// The chain reading the SOURCE, so the first hop is NOT a move: `t`
			// keeps its deep class, `v` is retained against and takes the
			// shallow row, and the second hop moves `v` into `u`. The struct
			// limb's source-read row is the one that told a half fix from a
			// whole one; this is the tuple pair's.
			name: "tuple_alias_chain_source_read",
			src: `function round(i: i32): i32 { var t: (i32, i32[]) = (i, [i, i + 1]); var v: (i32, i32[]) = t; var u: (i32, i32[]) = v; return t.1.len() + u.0; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			// The chain reading the MIDDLE link after the last bind, so the
			// second hop is a DUPLICATION whose source is a moved-into link: `v`
			// holds "TUPRCS:" alone, and the retain against it is the one the
			// old gate could not fire. `u` takes the shallow dec, `v` the deep
			// free, and the box needs the rc of 2 that retain provides.
			name: "tuple_alias_chain_middle_read",
			src: `function round(i: i32): i32 { var t: (i32, i32[]) = (i, [i, i + 1]); var v: (i32, i32[]) = t; var u: (i32, i32[]) = v; return u.1.len() + v.0; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			// THREE links: the deep class migrates twice, and every hop after
			// the first reads a "TUPRCS:"-only source.
			name: "tuple_alias_chain_three_links",
			src: `function round(i: i32): i32 { var t: (i32, i32[]) = (i, [i, i + 1]); var v: (i32, i32[]) = t; var u: (i32, i32[]) = v; var z: (i32, i32[]) = u; return z.1.len() + z.0; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			// The chain in an IF ARM. Moves are top-level only, so no surgery
			// happens here: every link retains and takes the shallow dec on the
			// taken path, and the source deep-frees unconditionally.
			name: "tuple_alias_chain_conditional",
			src: `function round(i: i32): i32 { var t: (i32, i32[]) = (i, [i, i + 1]); var n: i32 = 0; if (i % 2 == 0) { var v: (i32, i32[]) = t; var u: (i32, i32[]) = v; n = u.1.len(); } return n + t.1.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 200, frees: 200,
		},
		{
			// REFUSED: the LAST link is returned. All-or-nothing — the escape on
			// `u` costs `t` and `v` their credit too, since all three name the
			// box the caller now holds.
			name: "tuple_alias_chain_link_returned_refused",
			src: `function esc(i: i32): (i32, i32[]) { var t: (i32, i32[]) = (i, [i, i + 1]); var v: (i32, i32[]) = t; var u: (i32, i32[]) = v; return u; }
function round(i: i32): i32 { var r: (i32, i32[]) = esc(i); return r.1.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 4, allocs: 200, frees: 0,
		},
		{
			// REFUSED: a MIDDLE link is stored into a container that outlives
			// it. The 100 frees are `held`'s own buffer; the tuple's box and
			// element stay put.
			name: "tuple_alias_chain_middle_link_held_refused",
			src: `function sink(xs: (i32, i32[])[]): i32 { return xs.len(); }
function round(i: i32): i32 { var t: (i32, i32[]) = (i, [i, i + 1]); var v: (i32, i32[]) = t; var u: (i32, i32[]) = v; var held: (i32, i32[])[] = [v]; return u.1.len() + sink(held) + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 300, frees: 100,
		},
		{
			// The SCALAR tuple chain: no deep class, so a move performs no
			// surgery and each hop reads a "TUP:" source. This pair leaked
			// under the per-site rule (100/0) and was never at risk of the
			// over-release; it is here so the two limbs stand or fall together.
			name: "tuple_alias_scalar_chain",
			src: `function round(i: i32): i32 { var t: (i32, i32) = (i, i + 1); var v: (i32, i32) = t; var u: (i32, i32) = v; return u.0 + u.1; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 100, frees: 100,
		},
		{
			name: "tuple_alias_scalar_chain_middle_read",
			src: `function round(i: i32): i32 { var t: (i32, i32) = (i, i + 1); var v: (i32, i32) = t; var u: (i32, i32) = v; return u.0 + v.1; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 100, frees: 100,
		},
		{
			name: "tuple_alias_scalar_chain_three_links",
			src: `function round(i: i32): i32 { var t: (i32, i32) = (i, i + 1); var v: (i32, i32) = t; var u: (i32, i32) = v; var z: (i32, i32) = u; return z.0 + z.1; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 100, frees: 100,
		},
		{
			name: "tuple_alias_scalar_chain_conditional",
			src: `function round(i: i32): i32 { var t: (i32, i32) = (i, i + 1); var n: i32 = 0; if (i % 2 == 0) { var v: (i32, i32) = t; var u: (i32, i32) = v; n = u.1; } return n + t.0 + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 33, allocs: 100, frees: 100,
		},
		{
			// REFUSED: the last link returned, scalar limb.
			name: "tuple_alias_scalar_chain_link_returned_refused",
			src: `function esc(i: i32): (i32, i32) { var t: (i32, i32) = (i, i + 1); var v: (i32, i32) = t; var u: (i32, i32) = v; return u; }
function round(i: i32): i32 { var r: (i32, i32) = esc(i); return r.0 + r.1; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 100, frees: 0,
		},
		{
			// REFUSED: a middle link held by a container, scalar limb. The 100
			// frees are `held`'s buffer.
			name: "tuple_alias_scalar_chain_middle_link_held_refused",
			src: `function sink(xs: (i32, i32)[]): i32 { return xs.len(); }
function round(i: i32): i32 { var t: (i32, i32) = (i, i + 1); var v: (i32, i32) = t; var u: (i32, i32) = v; var held: (i32, i32)[] = [v]; return u.0 + sink(held) + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 200, frees: 100,
		},
		{
			// AN ENUM with an rc payload, aliased. This row pins the #7368
			// discrimination from the credit side: enum locals carry their enum
			// NAME in the same struct_type field a type test would have read, but
			// they earn "RCENUM:" / "SCENUMS:" rather than the struct credit, so
			// slot_is_reclaimable_struct refuses them.
			//
			// It used to be named "unchanged" and pin frees: 0 — the enum flavour
			// of the escape scan had never grown the #7282 alias forgiveness, so a
			// bind alone (even a DEAD one) denied the source its whole credit.
			// #7687 gives it that forgiveness, vetted through the enum gate and
			// refused when the alias hands its payload out, so the shape now
			// balances. Native is 100/100 here; the remaining alloc-count gap is a
			// volume divergence, not a reclaim one.
			name: "enum_alias_reclaimed",
			src: `enum E { A(i32[]), B }
function mke(i: i32): E { if (i % 2 == 0) { return E.A([i, i + 1]); } return E.B; }
function round(i: i32): i32 { var e: E = mke(i); var f: E = e; var n: i32 = 0; match (f) { E.A(k) => { n = k[0]; }, E.B => { n = 1; } } return n; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 10, allocs: 150, frees: 150,
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
			// The AS-PATTERN form of the same statement. `w @ P { .. }` binds w
			// to the whole scrutinee via build_struct_match's scrutinee cache —
			//     var __sm.._v = p;      <- alias level 1
			//     var w = __sm.._v;      <- alias level 2
			// — an ALIAS CHAIN, which the credit-side escape gate refused
			// conservatively (this row measured 100/0 then). The plan's verdict
			// (struct routing wave) forgives the chain for this SCALAR-ONLY
			// struct and the cell is clean; the rc-FIELD chain is still refused
			// The chain rule it desugars to is credited now (#7386), so this row
			// and struct_alias_chain above stand or fall together.
			name: "struct_as_pattern_binder",
			src: `struct P { x: i32, y: i32 }
function round(i: i32): i32 {
    var total: i32 = 0;
    var p: P = P { x: 3 + i, y: 4 + i };
    if let w @ P { x, y } = p { total = total + w.x + y; }
    return total;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 59, allocs: 100, frees: 100,
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
			want: 74, allocs: 100, frees: 100,
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
    var t: string = w("ab");
    var v: string = t;
    var n: i32 = v.len();
    return n + t.len() + i;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 72, allocs: 100, frees: 100,
		},
		{
			// A PARAMETER is borrowed, never owned, so aliasing one may not RETAIN:
			// the retain is gated on slot_is_reclaimable_str, whose first line
			// refuses a parameter, and the credit is only ever copied from a source
			// that already held one. That is unchanged and is what this row still
			// guards. An unconditional `is_str` in the retain once gave
			// `var sp: string = sep;` inside std/array's join_with_last an inc
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
function plen(p: string): i32 { var v: string = p; return v.len(); }
function round(i: i32): i32 { var t: string = w("ab"); return plen(t) + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
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
    var t: string = w("ab");
    if (i % 2 == 0) { var v: string = t; return v.len() + i; }
    return t.len() + i;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 100, frees: 100,
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
			want: 77, allocs: 100, frees: 100,
		},
		{
			// `xs.join(sep)`. Base: allocs=700 frees=500, 3200 live.
			name: "string_alias_join_producer",
			src: `import "std/array";
function round(i: i32): i32 { var xs: string[] = ["ab", "cd"]; var t: string = xs.join(","); var v: string = t; return v.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 55, allocs: 400, frees: 400,
		},
		{
			// `<string>.replace(old, new)`. Base: allocs=400 frees=200, 3200 live.
			name: "string_alias_replace_producer",
			src: `import "std/string";
function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { var s: string = w("aXb"); var t: string = s.replace("X", "Y"); var v: string = t; return v.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
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
function round(i: i32): i32 { var s: string = w("  ab  "); var t: str = s.trim(); var v: str = t; return v.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 55, allocs: 200, frees: 200,
		},
		{
			// REFUSED, and correctly so: a chain `var v = t; var u = v;` makes v itself
			// escape as a bare ident, so v is not an eligible alias site and t keeps no
			// credit either. Conservative — it leaks rather than over-releasing — and
			// pinned so that widening the alias set later has to face this case
			// deliberately instead of discovering it as a double free.
			name: "string_alias_chain",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { var t: string = w("ab"); var v: string = t; var u: string = v; return u.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 100, frees: 100,
		},
		{
			// REFUSED: a REASSIGNED alias does not hold the box the credit describes
			// at exit, so alias_bind_sites_of excludes it (body_assign_targets).
			name: "string_alias_reassigned_refused",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { var t: string = w("ab"); var v: string = t; v = w("cd"); return v.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 200, frees: 0,
		},
		{
			// REFUSED, as a property of the class: a string-builder ACCUMULATOR is
			// reassigned by definition and each rebind frees the box it supersedes, so
			// an alias bound before a rebind would point at freed memory. Every wired
			// family carries `index_of_str(reassigned, …) < 0`, so no route grants one.
			name: "string_accumulator_alias_refused",
			src: `function round(i: i32): i32 { var s: string = ""; var k: i32 = 0; while (k < 3) { s = s + "x"; k = k + 1; } var v: string = s; return v.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 21, allocs: 300, frees: 0,
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
			want: 21, allocs: 200, frees: 100,
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
			want: 57, allocs: 200, frees: 0,
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
			want: 68, allocs: 300, frees: 300,
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
    var src: string[] = [mkstr("x"), mkstr("y")];
    var x: string[] = src;
    return (x[0][0] as i32 + src[1][0] as i32 + i) % 101;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 32, allocs: 300, frees: 300,
		},
		{
			// The CHAIN, credited as one set (#7750). It used to be refused —
			// `var y = x` is a bare-ident bind the per-site forgiveness list
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
    var src: string[] = [mkstr("x")];
    var x: string[] = src;
    var y: string[] = x;
    return (x.len() + y.len() + src.len() + i) % 101;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68, allocs: 200, frees: 200,
		},
		{
			// REFUSED: an ELEMENT escapes from a MIDDLE link. A string[]'s
			// release walks the elements, so this is what the strarr gate is
			// substituted for — the plain walker cannot see it, and the deep
			// free would dangle `e`.
			name: "strarr_alias_chain_elem_escape_refused",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var src: string[] = [mkstr("x")];
    var x: string[] = src;
    var y: string[] = x;
    var e: string = x[0];
    return (y.len() + e.len() + i) % 101;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68, allocs: 200, frees: 100,
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
    var t: E = E.A([i, i + 1]);
    var v: E = t;
    var u: E = v;
    match (u) { E.A(a) => { return a.len() + i; }, E.B => { return i; } }
    return 0;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 4, allocs: 200, frees: 200,
		},
		{
			// REFUSED: a link hands the PAYLOAD out. The enum release deep-drops
			// it, so this is a use-after-free rather than a leak if admitted —
			// the condition rcenum_alias_bind_sites_of already applied per site,
			// now applied to every link.
			name: "enum_alias_chain_payload_out_refused",
			src: `enum E { A(i32[]), B }
function round(i: i32): i32 {
    var t: E = E.A([i, i + 1]);
    var v: E = t;
    var u: E = v;
    var out: i32[] = [0];
    match (u) { E.A(a) => { out = a; }, E.B => {} }
    return out.len() + i;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 4, allocs: 300, frees: 100,
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
			want: 67, allocs: 300, frees: 100,
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
function plen(p: string[]): i32 { var v: string[] = p; return v.len(); }
function round(i: i32): i32 { var src: string[] = [mkstr("x")]; return (plen(src) + i) % 101; }
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 70, allocs: 200, frees: 200,
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

			// Every over-release in this family balances the census, and the
			// underflow counter only sees the SECOND dec of a box — a deep free
			// that reads through a box the shallow dec already returned is a
			// use-after-free the counter reports late or not at all (the tuple
			// chain, #7750). The quarantining allocator reports both directly.
			// A refused row's leak is reported here too and is not a failure.
			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "alias_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, runner, sanBin)
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
