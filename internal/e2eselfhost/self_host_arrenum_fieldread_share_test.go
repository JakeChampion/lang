package e2eselfhost

import (
	"testing"
)

// --- The FIELD-READ spelling of the enum-array counted share -----------------
//
// `var p: P = P { f: q.f, … }` off a live sibling holder — the flatten__RewriteCtx
// shape, enum-array flavour, and the construction matrix's enum_arr__fieldread
// cell (600 allocs / 300 frees against native's 600/600). The read lowers via
// struct_get to the SOURCE box's buffer, so the new box co-owns it; the share was
// uncounted and the credit pass had, correctly for an uncounted read, marked BOTH
// holders box-only ("NODEEP:") — p because the field value reads as an un-retained
// borrow, q because a field read in a struct-literal field is a positive MOVE
// position. Retaining the read flips both verdicts at once.
//
// ONE predicate decides all three sites (enum_arr_field_share_read): the retain in
// the ExprStructLit lowering and the two marker flips in bind_var_slot. They agree
// by construction rather than by three conditions happening to line up — which is
// the whole safety argument, because a marker flipped without the inc behind it is
// a double free this class cannot see. Disabling the retain alone puts every case
// below at exit 99 with allocs == frees and live_bytes 0: the census reads clean
// while __rc_underflow_count fires.
//
// TWO PRECONDITIONS, both found by measurement rather than by reading:
//
//   - `respread`: a `T { ...base }` anywhere in the function copies every field
//     pointer into a fresh box with NO inc, minting a third owner. Two rc-gated
//     walks against a count of two is one release too many — exit 99 at a flat
//     700/700. Gated by FIELD TYPE (LowerState.spread_sites), not by holder name,
//     because the dangerous base can name a local with no slot yet at the moment
//     the share is decided.
//   - `blockscoped`: "NODEEP:" and "FLDCHECKED:" are the two arms of one either/or
//     verdict, and a block-scoped slot deep-drops ONLY on the second. Dropping
//     NODEEP alone left `p` with neither marker and the whole payload leaked
//     (600/300) while the flat sibling was clean — so flipping the verdict means
//     writing the witness, not just revoking the marker.
//
// `moved_ret` stays refused: at a move site the box takes over the local's
// reference and both the inc and the sweep dec are elided (#6726), so the shape
// measures exactly as it did before this slice. Every want was confirmed against
// BOTH oracles — bin/fern -interp and the native x86-64 backend agreed on each —
// never read off the self-host run under test.

const arrenumFieldReadDecl = `enum E { A(i32[]), B }
struct P { f: E[], n: i32 }
function mkv(i: i32): E[] { var o: E[] = []; o = o.append(E.A([i, i + 1])); return o; }
`

const arrenumFieldReadMain = `
function main(): i32 {
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`

func arrenumFieldReadCases() []arrenumShareCase {
	return []arrenumShareCase{
		{
			// The matrix cell: two live holders over one buffer.
			name: "basic",
			src: arrenumFieldReadDecl + `function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var t: i32 = 0;
    var p: P = P { f: q.f, n: i };
    t = p.f.len() + p.n;
    return (t + q.n + q.f.len()) % 101;
}` + arrenumFieldReadMain,
			want: 70, balance: true,
		},
		{
			// PRECONDITION 2. Identical to `basic` but for the braces, and that
			// alone took it to 600/300 while `basic` was clean.
			name: "blockscoped",
			src: arrenumFieldReadDecl + `function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var t: i32 = 0;
    if (i >= 0) { var p: P = P { f: q.f, n: i }; t = p.f.len() + p.n; }
    return (t + q.n + q.f.len()) % 101;
}` + arrenumFieldReadMain,
			want: 70, balance: true,
		},
		{
			// The share runs half the time, so the rounds that skip it exercise
			// the source's walk with no co-owner at all.
			name: "conditional",
			src: arrenumFieldReadDecl + `function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var t: i32 = 0;
    if (i % 2 == 0) { var p: P = P { f: q.f, n: i }; t = p.f.len() + p.n; }
    return (t + q.n + q.f.len()) % 101;
}` + arrenumFieldReadMain,
			want: 45, balance: true,
		},
		{
			// The new holder goes to a callee that may keep it; the source finding
			// rc > 1 simply declines its walk.
			name: "holder_escapes",
			src: arrenumFieldReadDecl + `function keepit(p: P): i32 { return p.f.len() + p.n; }
function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var p: P = P { f: q.f, n: i + 1 };
    var t: i32 = keepit(p);
    return (t + q.n + q.f.len()) % 101;
}` + arrenumFieldReadMain,
			want: 69, balance: true,
		},
		{
			// Three holders, each link counted: rc 3, and the walks hand off down
			// to the one that finds rc 1.
			name: "chain",
			src: arrenumFieldReadDecl + `function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var p: P = P { f: q.f, n: i + 1 };
    var z: P = P { f: p.f, n: i + 2 };
    return (q.f.len() + p.f.len() + z.f.len() + z.n) % 101;
}` + arrenumFieldReadMain,
			want: 66, balance: true,
		},
		{
			// PRECONDITION 1: the spread mints an uncounted third owner, so the
			// share is refused outright. Without the gate this is exit 99 at
			// 700 allocs, 700 frees, live_bytes 0.
			name: "respread",
			src: arrenumFieldReadDecl + `function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var p: P = P { f: q.f, n: i + 1 };
    var z: P = P { ...p, n: i + 2 };
    return (p.f.len() + z.n + q.n + z.f.len()) % 101;
}` + arrenumFieldReadMain,
			want: 68, balance: true,
		},
		{
			// The move-elided shape (#6726): no bind, so no marker flip, and the
			// inc goes with the move. Measures exactly as it did before the slice
			// — still the leak it was, deliberately.
			name: "moved_ret",
			src: arrenumFieldReadDecl + `function hold(i: i32): P {
    var q: P = P { f: mkv(i), n: i };
    return P { f: q.f, n: i };
}
function round(i: i32): i32 { var p: P = hold(i); return (p.f.len() + p.n) % 101; }` + arrenumFieldReadMain,
			want: 70,
		},
		{
			// The wrong-ANSWER probe. `p` dies at the end of the branch; `q` must
			// still read back intact after allocation churn has had the chance to
			// reuse anything freed early. The census is not the instrument here —
			// it agreed with native throughout the bug this catches.
			name: "source_uaf",
			src: arrenumFieldReadDecl + `function churn(i: i32): i32 {
    var a: i32[] = [i, i + 1, i + 2, i + 3];
    var b: i32[] = [i + 4, i + 5, i + 6, i + 7];
    return a[0] + b[3];
}
function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var t: i32 = 0;
    if (i % 2 == 0) {
        var p: P = P { f: q.f, n: i };
        t = p.f.len() + p.n;
    }
    var junk: i32 = churn(i * 7 + 3);
    var v: i32 = 0;
    match (q.f[0]) {
        E.A(xs) => { v = xs[0] + xs[1]; },
        E.B => { v = 0 - 1; }
    }
    if (v != i + i + 1) { return 0 - 1; }
    return (t + v) % 101;
}
function main(): i32 {
    var t: i32 = 0; var i: i32 = 0; var bad: i32 = 0;
    while (i < 200) { var r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 26, balance: true,
		},
	}
}

// TestSelfHostArrEnumFieldReadShareX86_64 — a struct-literal field READ of an
// enum-array is a counted share: the new box retains it and both holders trade
// their box-only marker for the deep walk, with the two shapes whose share count
// stays incomplete refusing it.
func TestSelfHostArrEnumFieldReadShareX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrenumFieldReadCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrenumfr_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 100 = the payload "+
					"read back wrong; 139 = it read freed memory)", tc.name, exit, tc.want)
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
		})
	}
}
