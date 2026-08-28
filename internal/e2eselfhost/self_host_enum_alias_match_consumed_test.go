package e2eselfhost

import (
	"strings"
	"testing"
)

// The confined-alias forgiveness reaching the rc-enum's MATCH-CONSUMED credit
// path. #7687 gave it to body_unsafe_for_enumfield_alias, which gates the
// exit-sweep credit; consumed_rcpayload_enum_frees consults a second scan
// (name_escapes_outside_stmt_enumfield) that had not learned it, so a dead
// alias bind still denied the source its match-consumed free.
//
// The bisect is why these cases are shaped as they are — see
// docs/rc-log/2026-08-28-enum-alias-match-consumed.md. Two matches on the
// source with NO alias are clean, and the alias alone denies the source, so the
// match is not the cause; the earlier diagnosis blamed the wrong gate. Both
// halves are kept as cases so a regression says which one moved.
//
// The forgiveness is keyed by the SAME proof as #7687
// (rcenum_alias_bind_sites_of), so its payload-out half still refuses the
// shapes that would over-release. Those cases gate on the exit code, not the
// census: an over-release here balances the census and shows up as
// __rc_underflow_count() (exit 99).
//
// Exits confirmed on BOTH oracles (bin/fern -interp and native x86-64).

func enumAliasMatchConsumedCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			// THE MINIMAL FAILING SHAPE: a dead alias, and the SOURCE is the
			// one consumed by a match. Was 200 allocs / 0 frees.
			name: "alias_bind_with_source_match",
			src: `enum E { Full(i32[]), None }
function round(i: i32): i32 {
    var src: E = E.Full([i, i + 1]);
    var x: E = src;
    var t: i32 = 0;
    match (src) { E.Full(ys) => { t = (t + ys.len()) % 101; }, E.None => {} }
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 34, balance: true,
		},
		{
			// The matrix cell: both the alias and the source are matched.
			name: "both_matched",
			src: `enum E { Full(i32[]), None }
function round(i: i32): i32 {
    var src: E = E.Full([i, i + 1]);
    var x: E = src;
    var t: i32 = 0;
    match (x) { E.Full(xs) => { t = t + xs.len(); }, E.None => {} }
    match (src) { E.Full(ys) => { t = (t + ys.len()) % 101; }, E.None => {} }
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68, balance: true,
		},
		{
			// The bisect's control: two matches on the source, NO alias. Was
			// already clean, and is kept so a regression distinguishes "the
			// alias forgiveness broke" from "the consuming-match free broke".
			name: "two_matches_no_alias_control",
			src: `enum E { Full(i32[]), None }
function round(i: i32): i32 {
    var src: E = E.Full([i, i + 1]);
    var t: i32 = 0;
    match (src) { E.Full(xs) => { t = t + xs.len(); }, E.None => {} }
    match (src) { E.Full(ys) => { t = (t + ys.len()) % 101; }, E.None => {} }
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68, balance: true,
		},
		{
			// THE FREE-SAFETY GUARD. The alias's arm moves the payload OUT, so
			// the source's deep release would free a buffer the frame still
			// holds. Refused by the shared proof's !enum_body_binds_rc_payload
			// half. #7687 measured this firing __rc_underflow when admitted —
			// with a balanced census either way — so the EXIT is the assertion
			// that matters here, not the frees.
			name: "payload_out_via_alias_refused",
			src: `enum E { Full(i32[]), None }
function round(i: i32): i32 {
    var src: E = E.Full([i, i + 1]);
    var x: E = src;
    var out: i32[] = [0];
    match (x) { E.Full(xs) => { out = xs; }, E.None => {} }
    match (src) { E.Full(ys) => { return (out.len() + ys.len()) % 101; }, E.None => {} }
    return out.len();
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68, wantFrees: 100,
		},
		{
			// The alias ESCAPES the frame (returned), so it is not confined and
			// the source keeps its refusal.
			name: "returned_alias_refused",
			src: `enum E { Full(i32[]), None }
function mk(i: i32): E {
    var src: E = E.Full([i, i + 1]);
    var x: E = src;
    match (src) { E.Full(ys) => { if (ys.len() == 99) { return E.None; } }, E.None => {} }
    return x;
}
function round(i: i32): i32 {
    var v: E = mk(i);
    var t: i32 = 0;
    match (v) { E.Full(zs) => { t = zs.len(); }, E.None => {} }
    return t;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 34, wantFrees: 0,
		},
	}
}

func TestSelfHostEnumAliasMatchConsumedX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range enumAliasMatchConsumedCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "enumaliasmc_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			// 99 is the assertion that matters on the refused rows: an
			// over-release balances the census, so only the underflow counter
			// distinguishes it from a correct free.
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
			sanBin := buildBin(t, gcc, dir, "enumaliasmc_san_"+tc.name, sanAsm)
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
