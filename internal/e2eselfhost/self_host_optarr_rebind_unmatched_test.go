package e2eselfhost

import (
	"testing"
)

// --- A rebound Option[i32[]] with nothing consuming it -----------------------
//
// `var x: Option[i32[]] = Some([i, i+1]); x = Some([i+2, i+3, i+4]);` measured
// 400 allocs / **0 frees** — nothing released at all, the signature of a credit
// that was never granted rather than a release that was half-wired. It is
// `opt_arr__rebind__unused` on both leak-matrix arches (#5338).
//
// THE FAMILY IS A 2x2 ON (reassigned, consumed-by-match) AND ONE CELL WAS
// EMPTY. collect_fresh_optarr_names requires the name to be reassigned AND a
// sole top-level match to consume it; collect_unmatched_optarr_names requires
// neither. A rebound local with nothing matching on it satisfied neither
// collector, so no one credited it and no one swept it. The matrix note read
// the other way round — "the release exists and only the no-match sweep half is
// missing" — but `oa_rebind_match` measuring clean says the release is fine;
// the admission is what was absent.
//
// The two collectors stay disjoint on the `reassigned` axis alone, which is
// what the comment they share relies on, so filling the quadrant inside
// collect_fresh_optarr_names cannot double-free anything.
//
// THE NO-MATCH PROOF IS NOT THE SIBLING'S. The never-reassigned class may lean
// on the plan's frame-escape verdict, because its locals are bound once. Asking
// the same question through it here produced a WRONG ANSWER rather than a leak:
// `option_escapes` returns the rebound local out of a callee, the plan granted
// it anyway, and the self-host exited 25 where native and interp both say 42.
// The fix is to ask body_unsafe_for directly, which is the same reason the
// matched branch carries name_escapes_outside_stmt. That row is the one below
// worth reading twice.
//
// Five shapes stay refused at their leaking counts — two matches on the name, a
// payload bound out of the match, the option escaping, an alias bound before
// the rebind, and a match placed BEFORE the rebind. Each reads its value back
// after 200 rounds of churn have recycled the freelist, and each answers
// identically on native x86-64, `bin/fern -interp` and the self-host.
//
// Every flipped row was re-run under FERN_SANITIZE=1 with
// FERN_RC_UNDERFLOW_TRAP=1 and FERN_RC_FREE_DEBUG=1: clean, no trap, no
// quarantine hit.

const optarrRebindChurn = `function churn(i: i32): i32 { var a: i32[] = [i, i + 1, i + 2]; var b: i32[] = [i, i + 1]; return a[0] + b[1]; }
`

const optarrRebindChurnMain = `
function main(): i32 {
    var acc: i32 = 0; var i: i32 = 0; var bad: i32 = 0;
    while (i < 200) { var r: i32 = round(i); if (r < 0) { bad = bad + 1; } acc = acc + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

const optarrRebindPlainMain = `
function main(): i32 {
    var acc: i32 = 0; var i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

func optarrRebindCases() []arrenumShareCase {
	return []arrenumShareCase{
		{
			// THE CELL, in the matrix's own spelling. 400/0 before.
			name: "rebind_unmatched",
			src: `function round(i: i32): i32 {
    var t: i32 = 0;
    var x: Option[i32[]] = Some([i, i + 1]);
    x = Some([i + 2, i + 3, i + 4]);
    t = t + 1;
    return t;
}` + optarrRebindPlainMain,
			want: 17, balance: true,
		},
		{
			// Control: the same rebind WITH a consuming match, clean before this
			// change. It is what says the release was never the missing half.
			name: "rebind_matched_unchanged",
			src: `function round(i: i32): i32 {
    var t: i32 = 0;
    var x: Option[i32[]] = Some([i, i + 1]);
    x = Some([i + 2, i + 3, i + 4]);
    match (x) { Some(xs) => { t = t + xs.len(); }, None => {} }
    t = t + 1;
    return t;
}` + optarrRebindPlainMain,
			want: 68, balance: true,
		},
		{
			// Control: the never-reassigned sibling, credited by the other
			// collector. If it moves, the quadrant fill reached past its axis.
			name: "single_bind_unchanged",
			src: `function round(i: i32): i32 {
    var t: i32 = 0;
    var x: Option[i32[]] = Some([i, i + 1]);
    t = t + 1;
    return t;
}` + optarrRebindPlainMain,
			want: 17, balance: true,
		},
		{
			name: "loop_rebind",
			src: optarrRebindChurn + `function round(i: i32): i32 {
    var t: i32 = 0;
    var x: Option[i32[]] = Some([i, i + 1]);
    var j: i32 = 0;
    while (j < 3) { x = Some([i + j, i + j + 1, i + j + 2]); j = j + 1; }
    var junk: i32 = churn(i);
    return (t + junk) % 101;
}` + optarrRebindChurnMain,
			want: 25, balance: true,
		},
		{
			name: "conditional_rebind",
			src: optarrRebindChurn + `function round(i: i32): i32 {
    var t: i32 = 0;
    var x: Option[i32[]] = Some([i, i + 1]);
    if (i % 2 == 0) { x = Some([i + 2, i + 3, i + 4]); }
    var junk: i32 = churn(i);
    return (t + junk) % 101;
}` + optarrRebindChurnMain,
			want: 25, balance: true,
		},
		{
			// THE SOUNDNESS ROW. The rebound local is RETURNED out of the
			// callee, so the frame must not release it. Crediting this through
			// the never-reassigned class's plan-backed escape gate made the
			// self-host answer 25 where native and interp say 42 — a wrong
			// answer, not a leak. It stays refused, and it stays 42.
			name: "refused_option_escapes",
			src: optarrRebindChurn + `function grab(i: i32): Option[i32[]] {
    var x: Option[i32[]] = Some([i, i + 1]);
    x = Some([i + 2, i + 3, i + 4]);
    return x;
}
function round(i: i32): i32 {
    var t: i32 = 0;
    var o: Option[i32[]] = grab(i);
    var junk: i32 = churn(i);
    match (o) { Some(xs) => { if (xs.len() != 3) { return 0 - 1; } t = t + xs[0]; }, None => { return 0 - 2; } }
    return (t + junk) % 101;
}` + optarrRebindChurnMain,
			want: 42,
		},
		{
			// REFUSED: two matches on the name, which
			// sole_top_level_match_idx reports the same way as none.
			name: "refused_two_matches",
			src: optarrRebindChurn + `function round(i: i32): i32 {
    var t: i32 = 0;
    var x: Option[i32[]] = Some([i, i + 1]);
    match (x) { Some(xs) => { t = t + xs.len(); }, None => {} }
    x = Some([i + 2, i + 3, i + 4]);
    match (x) { Some(ys) => { t = t + ys.len(); }, None => {} }
    var junk: i32 = churn(i);
    if (t < 2) { return 0 - 1; }
    return (t + junk) % 101;
}` + optarrRebindChurnMain,
			want: 51,
		},
		{
			// REFUSED: the payload is bound out of the match and outlives it.
			name: "refused_payload_escapes",
			src: optarrRebindChurn + `function round(i: i32): i32 {
    var t: i32 = 0;
    var keep: i32[] = [0];
    var x: Option[i32[]] = Some([i, i + 1]);
    x = Some([i + 2, i + 3, i + 4]);
    match (x) { Some(xs) => { keep = xs; }, None => {} }
    var junk: i32 = churn(i);
    if (keep.len() != 3) { return 0 - 1; }
    return (keep[0] + junk) % 101;
}` + optarrRebindChurnMain,
			want: 42,
		},
		{
			// REFUSED: an alias is bound before the rebind and matched after.
			name: "refused_alias_bind",
			src: optarrRebindChurn + `function round(i: i32): i32 {
    var t: i32 = 0;
    var x: Option[i32[]] = Some([i, i + 1]);
    var y: Option[i32[]] = x;
    x = Some([i + 2, i + 3, i + 4]);
    var junk: i32 = churn(i);
    match (y) { Some(ys) => { if (ys.len() != 2) { return 0 - 1; } t = t + ys[0]; }, None => { return 0 - 2; } }
    return (t + junk) % 101;
}` + optarrRebindChurnMain,
			want: 28,
		},
		{
			// REFUSED: the match precedes the rebind, so it consumes a value
			// the later store replaces — `match_idx > vi` is what rules it out.
			name: "refused_match_before_rebind",
			src: optarrRebindChurn + `function round(i: i32): i32 {
    var t: i32 = 0;
    var x: Option[i32[]] = Some([i, i + 1]);
    match (x) { Some(xs) => { t = t + xs.len(); }, None => {} }
    x = Some([i + 2, i + 3, i + 4]);
    var junk: i32 = churn(i);
    if (t < 2) { return 0 - 1; }
    return (t + junk) % 101;
}` + optarrRebindChurnMain,
			want: 39,
		},
	}
}

// TestSelfHostOptArrRebindUnmatchedX86_64 — a rebound Option[i32[]] with no
// consuming match is swept, and the escape it can still reach is refused.
func TestSelfHostOptArrRebindUnmatchedX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range optarrRebindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "optarrrebind_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 100 = a value read back "+
					"wrong; any other mismatch is a wrong ANSWER, which is how the escaping "+
					"option first showed up)", tc.name, exit, tc.want)
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
