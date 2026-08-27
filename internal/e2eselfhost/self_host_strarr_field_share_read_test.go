package e2eselfhost

import (
	"testing"
)

// --- A string[] FIELD READ handed into a string[] field ------------------------
//
// `var p: P = P { f: q.f, n: i }` — the RewriteCtx shape, string[] flavour, and
// the last leaking cell of the construction-retain matrix (#5338):
//
//	800 allocs / 300 frees, 16800 live, against native's 600/600
//
// Every other field kind was already counted here — `str__fieldread` through
// str_field_share_read, `enum_arr__fieldread` through enum_arr_field_share_read,
// `arr_i32__fieldread` and `struct__fieldread` through their own retains.
// `string[]` was the one left out.
//
// THE BLOCK WAS IN THE ADMISSION WALK, NOT AT THE SHARE POSITION. strarrfld_scan
// marks `<T>.<field>` for any field access, so the read of `q.f` refused P's
// string[]-field reclaim outright and NEITHER holder emitted __struct_drop_P or
// __field_reclaim_P — the identical `E[]` program emits both.
//
// Admitting the inline share is sound for the reason the bare-ident store is:
// the construction RETAINS an array field unconditionally, so the new holder
// co-owns a COUNTED reference and its drop's dec balances against it.
// `str_arr__local` measures that retain already working; the only difference
// here is that the value is read out of a sibling rather than out of a local.
// The read is also not walked as a read, so the SOURCE holder keeps its reclaim
// — a share needs both ends alive to balance.
//
// The HOISTED spelling (`var tt = q.f; P { f: tt }`) was left leaking here and
// is now closed too, by the local-BIND admission in
// self_host_strarr_field_bind_share_test.go. It needed a proof this position
// gets for free — that the bound local reaches nothing but the store — so it
// lives in that file with the rows refusing every other use of the local.
//
// THE FAILURE MODE HERE IS AN OVER-RELEASE, not a leak, which is what separates
// this cell from the five `__param` ones. str_field_share_read states it: "one
// box under two rc-aware k_str decs frees on the first and dangles on the
// second." So `escaping_holder` below is the load-bearing case rather than a
// formality: it returns the target holder while the source dies inside the
// callee, then reads every element back after 200 rounds of churn have recycled
// the freelist. It answers correctly on all three engines and is pinned at its
// LEAKING count — the conservative direction — because a holder that escapes is
// a different admission question this share does not answer.
//
// Every want was confirmed against native x86-64 AND `bin/fern -interp`, which
// agree on every exit, and the target was re-run under FERN_SANITIZE=1 with
// FERN_RC_UNDERFLOW_TRAP=1 and FERN_RC_FREE_DEBUG=1: clean, no trap, no
// quarantine hit.

const strarrShareReadDecl = `struct P { f: string[], n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string[] { var o: string[] = []; o = o.append(w("a")); o = o.append(w("b")); return o; }
`

func strarrShareReadCases() []arrenumShareCase {
	loop := `
function main(): i32 {
    var t: i32 = 0; var i: i32 = 0;
    while (i < 100) { t = t + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`
	return []arrenumShareCase{
		{
			// The cell. 800/300 before, 800/800 now.
			name: "inline_field_share",
			src: strarrShareReadDecl + `function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var p: P = P { f: q.f, n: i };
    return (p.f.len() + p.f[0].len() + p.n + q.n) % 101;
}` + loop,
			want: 72, balance: true,
		},
		{
			// Control: the bare-ident store, already clean before this change.
			// If this ever moves, the admission widened somewhere it should not.
			name: "local_store_unchanged",
			src: strarrShareReadDecl + `function round(i: i32): i32 {
    var src: string[] = mkv(i);
    var p: P = P { f: src, n: i };
    return (p.f.len() + p.f[0].len() + p.n) % 101;
}` + loop,
			want: 71, balance: true,
		},
		{
			// The HOISTED spelling, closed by the local-BIND admission in
			// self_host_strarr_field_bind_share_test.go, which owns the shape
			// and the rows refusing everything around it. Kept here as the
			// pair: the two programs differ only in whether the read is named,
			// so they must agree — on the exit AND on the accounting.
			name: "hoisted_bind_now_clean",
			src: strarrShareReadDecl + `function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var tt: string[] = q.f;
    var p: P = P { f: tt, n: i };
    return (p.f.len() + p.f[0].len() + p.n + q.n) % 101;
}` + loop,
			want: 72, balance: true,
		},
		{
			// THE SOUNDNESS CASE. The source holder dies inside `make` while the
			// target is returned, and every element is read back after churn has
			// recycled the freelist. An over-release here returns -1 or -2 (exit
			// 100) or segfaults (139); native and interp both exit 8. Pinned at
			// its LEAKING count: an escaping holder is a different admission
			// question, so the conservative direction is kept.
			name: "escaping_holder",
			src: strarrShareReadDecl + `function churn(i: i32): i32 { var a: string[] = mkv(i); var b: string[] = mkv(i + 1); return a[0].len() + b[1].len(); }
function make(i: i32): P { var q: P = P { f: mkv(i), n: i }; var p: P = P { f: q.f, n: i }; return p; }
function round(i: i32): i32 {
    var want: i32 = w("a").len();
    var p: P = make(i);
    var junk: i32 = churn(i * 3 + 1);
    if (p.f.len() != 2) { return 0 - 1; }
    if (p.f[0].len() != want) { return 0 - 2; }
    return (p.f[1].len() + junk) % 101;
}
function main(): i32 {
    var t: i32 = 0; var i: i32 = 0; var bad: i32 = 0;
    while (i < 200) { var r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 8,
		},
	}
}

// TestSelfHostStrArrFieldShareReadX86_64 — a bare `q.f` string[] read handed into
// a string[] field is a counted share, and both holders keep their reclaim.
func TestSelfHostStrArrFieldShareReadX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strarrShareReadCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "strarrshareread_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 100 = an element read back "+
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
				t.Errorf("%s: %s — pinned as LEAKING; if this now balances the pin is stale "+
					"and the row belongs to whatever closed it", tc.name, summary)
			}
		})
	}
}
