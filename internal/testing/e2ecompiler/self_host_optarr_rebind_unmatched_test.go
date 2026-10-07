package e2ecompiler

import (
	"testing"
)

// --- A rebound Option[i32[]] with nothing consuming it -----------------------
//
// `let x: Option[i32[]] = Some([i, i+1]); x = Some([i+2, i+3, i+4]);` must
// release both payloads although no match consumes the local; left uncredited
// it is 400 allocs / **0 frees**. It is `opt_arr__rebind__unused` on both
// leak-matrix arches (#5338). `rebind_matched_unchanged` is the matched control.
//
// A rebound local is bound more than once, so whether it escapes cannot be read
// off one binding: `refused_option_escapes` returns it out of a callee, and
// admitting it there gives a WRONG ANSWER rather than a leak (25 where the
// interpreter says 42). That row is the one below worth reading twice.
//
// Five shapes could over-release — two matches on the name, a payload bound out
// of the match, the option escaping, an alias bound before the rebind, and a
// match placed BEFORE the rebind. Each reads its value back after 200 rounds of
// churn have recycled the freelist and must answer as `bin/fern -interp` does.
// Every row balances.

const optarrRebindChurn = `function churn(i: i32): i32 { let a: i32[] = [i, i + 1, i + 2]; let b: i32[] = [i, i + 1]; return a[0] + b[1]; }
`

const optarrRebindChurnMain = `
function main(): i32 {
    let acc: i32 = 0; let i: i32 = 0; let bad: i32 = 0;
    while (i < 200) { let r: i32 = round(i); if (r < 0) { bad = bad + 1; } acc = acc + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

const optarrRebindPlainMain = `
function main(): i32 {
    let acc: i32 = 0; let i: i32 = 0;
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
    let t: i32 = 0;
    let x: Option[i32[]] = Some([i, i + 1]);
    x = Some([i + 2, i + 3, i + 4]);
    t = t + 1;
    return t;
}` + optarrRebindPlainMain,
			want: 17,
		},
		{
			// Control: the same rebind WITH a consuming match, clean before this
			// change. It is what says the release was never the missing half.
			name: "rebind_matched_unchanged",
			src: `function round(i: i32): i32 {
    let t: i32 = 0;
    let x: Option[i32[]] = Some([i, i + 1]);
    x = Some([i + 2, i + 3, i + 4]);
    match (x) { Some(xs) => { t = t + xs.len(); }, None => {} }
    t = t + 1;
    return t;
}` + optarrRebindPlainMain,
			want: 68,
		},
		{
			// Control: the never-reassigned sibling, credited by the other
			// collector. If it moves, the quadrant fill reached past its axis.
			name: "single_bind_unchanged",
			src: `function round(i: i32): i32 {
    let t: i32 = 0;
    let x: Option[i32[]] = Some([i, i + 1]);
    t = t + 1;
    return t;
}` + optarrRebindPlainMain,
			want: 17,
		},
		{
			name: "loop_rebind",
			src: optarrRebindChurn + `function round(i: i32): i32 {
    let t: i32 = 0;
    let x: Option[i32[]] = Some([i, i + 1]);
    let j: i32 = 0;
    while (j < 3) { x = Some([i + j, i + j + 1, i + j + 2]); j = j + 1; }
    let junk: i32 = churn(i);
    return (t + junk) % 101;
}` + optarrRebindChurnMain,
			want: 25,
		},
		{
			name: "conditional_rebind",
			src: optarrRebindChurn + `function round(i: i32): i32 {
    let t: i32 = 0;
    let x: Option[i32[]] = Some([i, i + 1]);
    if (i % 2 == 0) { x = Some([i + 2, i + 3, i + 4]); }
    let junk: i32 = churn(i);
    return (t + junk) % 101;
}` + optarrRebindChurnMain,
			want: 25,
		},
		{
			// THE SOUNDNESS ROW. The rebound local is RETURNED out of the
			// callee, so the frame must not release it; a release here
			// answered 25 where native and interp say 42.
			name: "refused_option_escapes",
			src: optarrRebindChurn + `function grab(i: i32): Option[i32[]] {
    let x: Option[i32[]] = Some([i, i + 1]);
    x = Some([i + 2, i + 3, i + 4]);
    return x;
}
function round(i: i32): i32 {
    let t: i32 = 0;
    let o: Option[i32[]] = grab(i);
    let junk: i32 = churn(i);
    match (o) { Some(xs) => { if (xs.len() != 3) { return 0 - 1; } t = t + xs[0]; }, None => { return 0 - 2; } }
    return (t + junk) % 101;
}` + optarrRebindChurnMain,
			want: 42,
		},
		{
			// Two matches on the name: neither is the sole consumer, and each
			// must read its own payload.
			name: "refused_two_matches",
			src: optarrRebindChurn + `function round(i: i32): i32 {
    let t: i32 = 0;
    let x: Option[i32[]] = Some([i, i + 1]);
    match (x) { Some(xs) => { t = t + xs.len(); }, None => {} }
    x = Some([i + 2, i + 3, i + 4]);
    match (x) { Some(ys) => { t = t + ys.len(); }, None => {} }
    let junk: i32 = churn(i);
    if (t < 2) { return 0 - 1; }
    return (t + junk) % 101;
}` + optarrRebindChurnMain,
			want: 51,
		},
		{
			// The payload is bound out of the match and outlives it.
			name: "refused_payload_escapes",
			src: optarrRebindChurn + `function round(i: i32): i32 {
    let t: i32 = 0;
    let keep: i32[] = [0];
    let x: Option[i32[]] = Some([i, i + 1]);
    x = Some([i + 2, i + 3, i + 4]);
    match (x) { Some(xs) => { keep = xs; }, None => {} }
    let junk: i32 = churn(i);
    if (keep.len() != 3) { return 0 - 1; }
    return (keep[0] + junk) % 101;
}` + optarrRebindChurnMain,
			want: 42,
		},
		{
			// An alias is bound before the rebind and matched after.
			name: "refused_alias_bind",
			src: optarrRebindChurn + `function round(i: i32): i32 {
    let t: i32 = 0;
    let x: Option[i32[]] = Some([i, i + 1]);
    let y: Option[i32[]] = x;
    x = Some([i + 2, i + 3, i + 4]);
    let junk: i32 = churn(i);
    match (y) { Some(ys) => { if (ys.len() != 2) { return 0 - 1; } t = t + ys[0]; }, None => { return 0 - 2; } }
    return (t + junk) % 101;
}` + optarrRebindChurnMain,
			want: 28,
		},
		{
			// The match precedes the rebind, so it consumes a value
			// the later store replaces.
			name: "refused_match_before_rebind",
			src: optarrRebindChurn + `function round(i: i32): i32 {
    let t: i32 = 0;
    let x: Option[i32[]] = Some([i, i + 1]);
    match (x) { Some(xs) => { t = t + xs.len(); }, None => {} }
    x = Some([i + 2, i + 3, i + 4]);
    let junk: i32 = churn(i);
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
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
		})
	}
}
