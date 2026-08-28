package e2eselfhost

import (
	"strings"
	"testing"
)

// A DEAD alias bind used to kill an rc-enum local's whole reclaim credit
// (#7687): `var x: E = src;` — with `x` never read — took `src` from
// 200 allocs / 200 frees to 200 / 0, 8000 live, against native's 200/200.
// The match the leak-matrix rows blamed was irrelevant; the bind alone did it.
//
// THE OBVIOUS FIX IS A MEASURED USE-AFTER-FREE, which is why every row here
// gates on the EXIT CODE (each program returns 99 from `__rc_underflow()`)
// rather than on bytes: forgiving the alias lets the SOURCE keep its deep
// credit, and if the alias hands the payload out, that credit frees a buffer
// the caller still holds. The census stays balanced while it happens, so the
// counter is the only witness. Every `want` is native's own answer.
//
// Three things make the forgiveness safe, and each has a row:
//
//  1. the alias is vetted through the ENUM gate (body_unsafe_for_enumfield),
//     not the coarse walker, so an escaping alias is refused;
//  2. it is additionally refused when the alias hands its PAYLOAD out
//     (`match (x) { Full(xs) => xs }`) — the escape scan reads a bare-ident
//     scrutinee as a borrow, which is true of the BOX and false of the
//     payload. `payload_out_via_alias_refused` is that row, and it fails with
//     exit 99 without the check;
//  3. every enum release is rc-gated, so where two owners do exist only the
//     last one walks the payload (emit_enum_variant_drops no longer bypasses
//     the `__fern_rc_is_unique` guard — five call sites did).

func rcenumAliasBindCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			// The issue's repro: the alias is never read at all.
			name: "dead_alias_bind",
			src: `enum E { Full(i32[]), None }
function round(i: i32): i32 { var src: E = E.Full([i, i + 1]); var x: E = src; var t: i32 = 7; return t; }
function main(): i32 {
    var s: i32 = 0; var r: i32 = 0;
    while (r < 100) { s = s + round(r); r = r + 1; }
    if (__rc_underflow() != 0) { return 99; }
    return s % 97;
}`,
			want: 21, balance: true,
		},
		{
			// The alias CONSUMED by a match, borrow-only. The matrix rows
			// (`enum_rc_payload__fnscope__alias_match`) are this shape.
			name: "alias_matched_borrow_only",
			src: `enum E { Full(i32[]), None }
function round(i: i32): i32 { var src: E = E.Full([i, i + 1]); var x: E = src; match (x) { Full(xs) => { return xs.len(); }, None => { return 0; } } }
function main(): i32 {
    var s: i32 = 0; var r: i32 = 0;
    while (r < 100) { s = s + round(r); r = r + 1; }
    if (__rc_underflow() != 0) { return 99; }
    return s % 97;
}`,
			want: 6, balance: true,
		},
		{
			// The source REBOUND while the alias is live: the rebind releases
			// the superseded box, so this is the row that would over-release
			// first if the rc gate were not arbitrating.
			name: "source_rebound_alias_live",
			src: `enum E { Full(i32[]), None }
function round(i: i32): i32 { var src: E = E.Full([i, i + 1]); var x: E = src; src = E.Full([i + 2]); match (x) { Full(xs) => { return xs.len(); }, None => { return 0; } } }
function main(): i32 {
    var s: i32 = 0; var r: i32 = 0;
    while (r < 100) { s = s + round(r); r = r + 1; }
    if (__rc_underflow() != 0) { return 99; }
    return s % 97;
}`,
			want: 6, balance: true,
		},
		{
			// Handed to a borrowing callee.
			name: "alias_passed_to_callee",
			src: `enum E { Full(i32[]), None }
function peek(e: E): i32 { match (e) { Full(xs) => { return xs.len(); }, None => { return 0; } } }
function round(i: i32): i32 { var src: E = E.Full([i, i + 1]); var x: E = src; return peek(x); }
function main(): i32 {
    var s: i32 = 0; var r: i32 = 0;
    while (r < 100) { s = s + round(r); r = r + 1; }
    if (__rc_underflow() != 0) { return 99; }
    return s % 97;
}`,
			want: 6, balance: true,
		},
		{
			// THE NON-VACUITY INSTRUMENT. The alias's match binds the rc payload
			// and RETURNS it, so the payload outlives the frame while the
			// source's deep release would free it. Refused: the shape keeps its
			// pre-existing leak. Without the payload-escape check this row
			// exits 99 — an over-release with a perfectly balanced census, which
			// is the trap this issue documents.
			name: "payload_out_via_alias_refused",
			src: `enum E { Full(i32[]), None }
function g(i: i32): i32[] { var src: E = E.Full([i, i + 1]); var x: E = src; match (x) { Full(xs) => { return xs; }, None => { return [0]; } } }
function round(i: i32): i32 { var v: i32[] = g(i); return v.len(); }
function main(): i32 {
    var s: i32 = 0; var r: i32 = 0;
    while (r < 100) { s = s + round(r); r = r + 1; }
    if (__rc_underflow() != 0) { return 99; }
    return s % 97;
}`,
			want: 6, balance: false, wantFrees: 100,
		},
		{
			// The alias itself escapes by return — refused for the plain reason,
			// and the control proving the vetting is not simply forgiving
			// everything.
			name: "alias_returned_refused",
			src: `enum E { Full(i32[]), None }
function mk(i: i32): E { var src: E = E.Full([i, i + 1]); var x: E = src; return x; }
function round(i: i32): i32 { var v: E = mk(i); match (v) { Full(xs) => { return xs.len(); }, None => { return 0; } } }
function main(): i32 {
    var s: i32 = 0; var r: i32 = 0;
    while (r < 100) { s = s + round(r); r = r + 1; }
    if (__rc_underflow() != 0) { return 99; }
    return s % 97;
}`,
			want: 6, balance: false, wantFrees: 0,
		},
	}
}

func TestSelfHostRcEnumAliasBindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range rcenumAliasBindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "rcenumalias_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d — 99 means __rc_underflow() fired: the alias forgiveness "+
					"handed the source a credit that frees a payload someone else still holds. The census "+
					"below balances either way, so the exit code is the whole signal.\n%s",
					tc.name, exit, tc.want, leakSummaryLine(stderr))
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
			if tc.balance {
				if live != 0 || allocs != frees {
					t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
				}
			} else {
				if live == 0 {
					t.Errorf("%s: %s — a REFUSED shape came back clean. If the forgiveness was widened to "+
						"cover it, that widening owns this row and needs its own over-release measurement",
						tc.name, summary)
				}
				if frees != tc.wantFrees {
					t.Errorf("%s: frees=%d, want %d — a moved count on a refused row is a silent widening", tc.name, frees, tc.wantFrees)
				}
			}

			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "rcenumalias_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "use-after-free") || strings.Contains(sanErr, "rc over-release") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}
}
