package e2eselfhost

import (
	"strings"
	"testing"
)

// The Option twin of the rc-enum alias-match fix: a CONFINED alias bind no
// longer denies the source its consuming-match free. This closed the last row
// where the self-host leaked against a clean native.
//
// The confinement proof (rcopt_alias_bind_sites_of) has two halves that are
// only sound together:
//
//	body_unsafe_for_MATCH_BORROW — the plain scan flags any bare ident, so an
//	  alias consumed by its own `match (x)` reads as an escape and every
//	  actually-used alias is refused (measured: the cell stayed leaking);
//	opt_body_binds_rc_payload — reading a match scrutinee as a borrow is true
//	  of the BOX and false of the PAYLOAD, so an alias whose arm carries the
//	  payload out must still be refused.
//
// EVERY case here gates on the EXIT CODE, and the refused ones deliberately do
// not assert balance. Removing the payload-out half makes
// payload_out_via_alias exit 99 with allocs=300 frees=300 live_bytes=0 — a
// PERFECTLY BALANCED census on a build that over-releases, cleaner-looking than
// the correct build's 300/200 with 4000 live. A balance assertion would pass
// the unsafe build and fail the safe one.
// See docs/rc-log/2026-08-28-option-alias-match-consumed.md.
//
// Exits confirmed on BOTH oracles (bin/fern -interp and native x86-64).

func optionAliasMatchConsumedCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			// The matrix cell opt_arr__fnscope__alias_match: alias matched,
			// source matched, both borrow-only.
			name: "alias_and_source_matched",
			src: `function round(i: i32): i32 {
    var src: Option[i32[]] = Some([i, i + 1]);
    var x: Option[i32[]] = src;
    var t: i32 = 0;
    match (x) { Some(xs) => { t = t + xs.len(); }, None => {} }
    match (src) { Some(ys) => { t = (t + ys.len()) % 101; }, None => {} }
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68, balance: true,
		},
		{
			// A DEAD alias with the source matched — the shape the plain
			// escape scan already handled once match-borrow was not needed.
			// Kept so a regression distinguishes the two halves.
			name: "dead_alias_source_matched",
			src: `function round(i: i32): i32 {
    var src: Option[i32[]] = Some([i, i + 1]);
    var x: Option[i32[]] = src;
    var t: i32 = 0;
    match (src) { Some(ys) => { t = (t + ys.len()) % 101; }, None => {} }
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 34, balance: true,
		},
		{
			// The alias handed to a BORROWING callee stays confined.
			name: "alias_to_borrowing_callee",
			src: `function peek(o: Option[i32[]]): i32 { match (o) { Some(v) => { return v.len(); }, None => {} } return 0; }
function round(i: i32): i32 {
    var src: Option[i32[]] = Some([i, i + 1]);
    var x: Option[i32[]] = src;
    var t: i32 = peek(x);
    match (src) { Some(ys) => { t = (t + ys.len()) % 101; }, None => {} }
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68, wantFrees: 0,
		},
		{
			// THE FREE-SAFETY GUARD, and the reason this file asserts exits.
			// The alias's arm moves the payload out, so the source's release
			// would free a buffer the frame still holds. With
			// opt_body_binds_rc_payload removed this exits 99 with a balanced
			// census — do NOT convert this to a balance assertion.
			name: "payload_out_via_alias_refused",
			src: `function round(i: i32): i32 {
    var src: Option[i32[]] = Some([i, i + 1]);
    var x: Option[i32[]] = src;
    var out: i32[] = [0];
    match (x) { Some(xs) => { out = xs; }, None => {} }
    match (src) { Some(ys) => { return (out.len() + ys.len()) % 101; }, None => {} }
    return out.len();
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68, wantFrees: 200,
		},
		{
			// The alias ESCAPES the frame; not confined, source keeps its
			// refusal.
			name: "returned_alias_refused",
			src: `function mk(i: i32): Option[i32[]] {
    var src: Option[i32[]] = Some([i, i + 1]);
    var x: Option[i32[]] = src;
    match (src) { Some(ys) => { if (ys.len() == 99) { return None; } }, None => {} }
    return x;
}
function round(i: i32): i32 {
    var v: Option[i32[]] = mk(i);
    var t: i32 = 0;
    match (v) { Some(zs) => { t = zs.len(); }, None => {} }
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 34, wantFrees: 0,
		},
		{
			// A REASSIGNED alias is not confined.
			name: "reassigned_alias_refused",
			src: `function round(i: i32): i32 {
    var src: Option[i32[]] = Some([i, i + 1]);
    var x: Option[i32[]] = src;
    var t: i32 = 0;
    x = Some([i, i + 2, i + 3]);
    match (src) { Some(ys) => { t = (t + ys.len()) % 101; }, None => {} }
    match (x) { Some(xs) => { t = (t + xs.len()) % 101; }, None => {} }
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 2, wantFrees: 0,
		},
	}
}

func TestSelfHostOptionAliasMatchConsumedX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range optionAliasMatchConsumedCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "optaliasmc_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			// THE assertion. An over-release here balances the census, so the
			// exit code is the only thing that sees it.
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 139 = read freed memory)", tc.name, exit, tc.want)
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
			} else if frees != tc.wantFrees {
				t.Errorf("%s: %s — refused row's frees moved (want %d)", tc.name, summary, tc.wantFrees)
			}

			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "optaliasmc_san_"+tc.name, sanAsm)
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
