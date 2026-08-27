package e2eselfhost

import (
	"testing"
)

// --- A string[] local read by `for s in names` -------------------------------
//
// `var names: string[] = [mk("a"), mk("b")]; for s in names { t = t + s.len(); }`
// measured 500 allocs / 100 frees against native's 100/100 — the whole array and
// both element boxes surviving every round. It is `for_in_str_elem__loop__read`
// on both leak-matrix arches (#5338, #7292/#7356 family).
//
// THE BINDER NEVER NEEDED A CREDIT. The matrix note reads "the for-in binder is
// not a StmtVar, so no collector credits it", which points at the wrong value:
// `s` borrows an element that `names` owns, and `names`'s own deep free is what
// releases it. What actually happened is that `strarr_unsafe_for`'s StmtFor arm
// refused the ARRAY's credit outright whenever the array was iterated, so the
// release never ran for either.
//
// The indexed sibling says so directly. Before this change:
//
//	for s in names { t = t + s.len(); }                        500/100
//	while (j < names.len()) { t = t + names[j].len(); j = j+1 } 500/500 clean
//
// Two spellings of one read, and only the `for` one leaked —
// `strarr_expr_unsafe` already draws the transient-versus-lasting line for
// `names[j]` in exactly those positions.
//
// So the arm asks the same question of the BINDER: body_unsafe_for over the
// loop body decides whether `s` outlives an iteration. A bind, a return, a
// container or struct store, or a call whose result is bound outward all make
// the loop unsafe again, and each of those is pinned below at its leaking
// count. A binder that SHADOWS the array is refused outright — the walk would
// otherwise read a different value under the same spelling.
//
// Every probe answers identically on native x86-64, `bin/fern -interp` and the
// self-host, before and after, and the refusing ones read their value back
// after 200 rounds of churn have recycled the freelist. The flipped rows were
// re-run under FERN_SANITIZE=1 with FERN_RC_UNDERFLOW_TRAP=1 and
// FERN_RC_FREE_DEBUG=1: no trap, no quarantine hit.

const forinBinderDecl = `function mkstr(a: string): string { return a + "-long-enough-to-heap-allocate"; }
function churn(i: i32): i32 { var a: string[] = [mkstr("c"), mkstr("d")]; return a[0].len() + a[1].len(); }
function keepstr(x: string): i32 { return x.len(); }
function stash(x: string): string { return x; }
`

const forinBinderChurnMain = `
function main(): i32 {
    var acc: i32 = 0; var i: i32 = 0; var bad: i32 = 0;
    while (i < 200) { var r: i32 = round(i); if (r < 0) { bad = bad + 1; } acc = acc + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

func forinBinderCases() []arrenumShareCase {
	plain := "\nfunction main(): i32 { var acc: i32 = 0; var i: i32 = 0; " +
		"while (i < 100) { acc = acc + round(i); i = i + 1; } " +
		"if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }\n"
	return []arrenumShareCase{
		{
			// THE CELL, in the matrix's own spelling. 500/100 before.
			name: "forin_len_read",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var names: string[] = [mkstr("a"), mkstr("b")];
    var t: i32 = 0;
    for s in names { t = (t + s.len()) % 101; }
    return t;
}` + plain,
			want: 68, balance: true,
		},
		{
			// Control: the indexed spelling of the same read, clean before this
			// change. It is what says the array's credit — not the binder's —
			// was the thing being refused.
			name: "indexed_read_unchanged",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var names: string[] = [mkstr("a"), mkstr("b")];
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < names.len()) { t = (t + names[j].len()) % 101; j = j + 1; }
    return t;
}` + plain,
			want: 68, balance: true,
		},
		{
			// The binder handed to a call that returns a SCALAR — the callee
			// keeps nothing, so the borrow is still transient.
			name: "binder_scalar_call_arg",
			src: forinBinderDecl + `function round(i: i32): i32 {
    var names: string[] = [mkstr("a"), mkstr("b")];
    var t: i32 = 0;
    for s in names { t = t + keepstr(s); }
    var junk: i32 = churn(i);
    if (t < 20) { return 0 - 1; }
    return (t + junk) % 101;
}` + forinBinderChurnMain,
			want: 65, balance: true,
		},
		{
			// REFUSED: the binder is assigned to a local that outlives the loop.
			name: "refused_binder_escapes_local",
			src: forinBinderDecl + `function round(i: i32): i32 {
    var want: i32 = mkstr("a").len();
    var names: string[] = [mkstr("a"), mkstr("b")];
    var keep: string = "";
    for s in names { keep = s; }
    var junk: i32 = churn(i);
    if (keep.len() != want) { return 0 - 1; }
    return (keep.len() + junk) % 101;
}` + forinBinderChurnMain,
			want: 72,
		},
		{
			// REFUSED: the binder is bound to a fresh local inside the body and
			// carried out through it.
			name: "refused_binder_bound_local",
			src: forinBinderDecl + `function round(i: i32): i32 {
    var want: i32 = mkstr("a").len();
    var names: string[] = [mkstr("a"), mkstr("b")];
    var last: string = "";
    for s in names { var t2: string = s; last = t2; }
    var junk: i32 = churn(i);
    if (last.len() != want) { return 0 - 1; }
    return (last.len() + junk) % 101;
}` + forinBinderChurnMain,
			want: 72,
		},
		{
			// REFUSED: the binder is stored into a container.
			name: "refused_binder_into_container",
			src: forinBinderDecl + `function round(i: i32): i32 {
    var want: i32 = mkstr("a").len();
    var names: string[] = [mkstr("a"), mkstr("b")];
    var out: string[] = [];
    for s in names { out = out.append(s); }
    var junk: i32 = churn(i);
    if (out[0].len() != want) { return 0 - 1; }
    return (out.len() + junk) % 101;
}` + forinBinderChurnMain,
			want: 33,
		},
		{
			// REFUSED: the binder leaves the frame as a return value.
			name: "refused_binder_returned",
			src: forinBinderDecl + `function grab(i: i32): string {
    var names: string[] = [mkstr("a"), mkstr("b")];
    for s in names { return s; }
    return mkstr("z");
}
function round(i: i32): i32 {
    var want: i32 = mkstr("a").len();
    var g: string = grab(i);
    var junk: i32 = churn(i);
    if (g.len() != want) { return 0 - 1; }
    return (g.len() + junk) % 101;
}` + forinBinderChurnMain,
			want: 72,
		},
		{
			// REFUSED: a call LAUNDERS the binder — it takes the string and
			// returns it, and the result is bound outward. The scalar-returning
			// case above is admitted; this one is the reason that distinction
			// has to be drawn on where the RESULT goes.
			name: "refused_call_launders_binder",
			src: forinBinderDecl + `function round(i: i32): i32 {
    var want: i32 = mkstr("a").len();
    var names: string[] = [mkstr("a"), mkstr("b")];
    var kept: string = "";
    for s in names { kept = stash(s); }
    var junk: i32 = churn(i);
    if (kept.len() != want) { return 0 - 1; }
    return (kept.len() + junk) % 101;
}` + forinBinderChurnMain,
			want: 72,
		},
		{
			// REFUSED: the binder SHADOWS the array. Admitting it would have the
			// walk read a different value under the same spelling.
			name: "refused_binder_shadows_array",
			src: forinBinderDecl + `function round(i: i32): i32 {
    var names: string[] = [mkstr("a"), mkstr("b")];
    var t: i32 = 0;
    for names in names { t = (t + names.len()) % 101; }
    var junk: i32 = churn(i);
    return (t + junk) % 101;
}` + forinBinderChurnMain,
			want: 65,
		},
	}
}

// TestSelfHostForInStrArrBinderX86_64 — iterating a reclaimable string[] with a
// transient binder keeps the array's deep credit, and a binder that outlives an
// iteration still takes it away.
func TestSelfHostForInStrArrBinderX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range forinBinderCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "forinbinder_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 100 = a value read back "+
					"wrong, i.e. an over-release; 139 = it read freed memory)", tc.name, exit, tc.want)
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
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
			if !tc.balance && live == 0 && allocs == frees {
				t.Errorf("%s: %s — pinned as REFUSED; if this now balances the credit "+
					"widened, and the row belongs to whatever widened it", tc.name, summary)
			}
		})
	}
}
