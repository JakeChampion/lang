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
// THE MODEL IS DUPLICATION, NOT TRANSFER. Both slots own a counted reference and
// both release it; the refcount arbitrates. `alias_in_a_conditional` is why:
// under a transfer model `if (c) { var v = t; }` leaves the source un-swept on
// the path where no transfer happened, so a leak becomes branch-dependent —
// strictly worse than the leak it replaces. Duplication emits the inc and the
// dec on the same path by construction.
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
			// STRUCT IS NOT IN THIS CHANGE, and this row pins that it is left
			// leaking rather than half-fixed. The bind-side retain fires only for
			// slot_is_rc_container (array / string / tuple); the clause that tried
			// to add structs keyed on `struct_type_of_slot`, which is ALSO set for
			// enum names and dyn tags, so it retained values that are not rc box
			// pointers — six integer fixtures and both differential shards
			// segfaulted, none of them touching a container alias.
			//
			// The two halves are coupled and must leave together: crediting the
			// alias without the retain double-frees at rc 1 (measured, exit 99),
			// which is a strictly worse state than the leak. So the row asserts
			// the ORIGINAL leak and, more importantly, that it does not become an
			// over-release while it waits.
			name: "struct_alias_still_leaks",
			src: `struct P { xs: i32[] }
function round(i: i32): i32 {
    var t: P = P { xs: [i, i + 1] };
    var v: P = t;
    return v.xs[0] + v.xs[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40, allocs: 200, frees: 0,
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
			// `<string>.trim()` binds a `str` — a borrowed VIEW, whose box carries a
			// NEGATIVE rc. Both __fern_rc_inc and __fern_str_free bail on the sign, so
			// the retain and the alias' release are each no-ops and only the source's
			// __fern_str_view_free reclaims the box: the alias is safe because of the
			// box's rc word, not because of the slot's str_view_local flag (which an
			// alias does not inherit).
			//
			// PARTIAL, and pinned as measured rather than as wished for: 200 → 250
			// frees of 300 allocs, 2400 → 1200 live. The view class keeps a leak this
			// change does not close, and the row exists so that it cannot silently
			// become an over-release.
			name: "string_alias_trim_view_partial",
			src: `import "std/string";
function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { var s: string = w("  ab  "); var t: str = s.trim(); var v: str = t; return v.len() + i; }
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 55, allocs: 300, frees: 250,
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
