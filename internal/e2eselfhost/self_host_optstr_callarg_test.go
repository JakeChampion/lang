package e2eselfhost

import (
	"strings"
	"testing"
)

// The OPTSTR credit learns the string-fresh registry
// (unmatched_optstr_payload_is_fresh): `Some(mk("abc"))` with mk a
// str_fresh_ret_fns_of-registered producer now earns the "OPTSTR:" credit the
// plan side already granted, so the box + payload sweep runs — the
// opt_str__callarg__read floor closes. The registry's own fixpoint keeps an
// aliased producer (`wrap(s) { return Some(s); }` and every param/field
// return) out, which the refused pins in
// self_host_unmatched_optstr_reclaim_test.go continue to assert.
//
// Exits confirmed against BOTH oracles (bin/fern -interp and native x86-64).
// Each case re-runs under FERN_SANITIZE=1 (identical exit, no over-release /
// use-after-free), and the flipped cell also re-compiles with
// FERN_SELFHOST_RC_PLAN=0, where the escape gate reverts to body_unsafe_for
// and the cell must fall back to its old safe leak — same exit, never a
// polarity change.

func optstrCallargCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			// The matrix cell opt_str__callarg__read verbatim.
			name: "callarg_producer_payload",
			src: `function mk(a: string): string { return a + "!"; }
function peek(o: Option[string]): i32 {
    match (o) { Some(s) => { return s.len(); }, None => { return 0; } }
}
function round(i: i32): i32 {
    var o: Option[string] = Some(mk("abc"));
    return peek(o) + i % 3;
}
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 14, balance: true,
		},
		{
			// The freed-block-reuse net: 200 rounds of same-size string churn
			// between the sweeps, and a kept string read back by CONTENT at
			// the end — a sweep that freed the wrong buffer surfaces here (or
			// in the sanitize leg's quarantine), not in the census.
			name: "callarg_churn_read_back",
			src: `function mk(a: string): string { return a + "!"; }
function peek(o: Option[string]): i32 {
    match (o) { Some(s) => { return s.len(); }, None => { return 0; } }
}
function round(i: i32): i32 {
    var o: Option[string] = Some(mk("abcdefgh"));
    var churn: string = mk("zzzzzzzz");
    return peek(o) + churn.len() + i % 3;
}
function main(): i32 {
    var keep: string = mk("keepmeeee");
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    var ok: i32 = 0;
    if (keep == "keepmeeee!") { ok = 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return (t + ok) % 97;
}`,
			want: 17, balance: true,
		},
	}
}

func TestSelfHostOptStrCallargX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range optstrCallargCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "optstrcallarg_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
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
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}

			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "optstrcallarg_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}

	// Plan-off leg on the flipped cell: the escape gate reverts to
	// body_unsafe_for, which reads the call arg as an escape, so the credit
	// is withheld and the cell reverts to its old safe leak — the exit must
	// not move and nothing may over-release.
	t.Run("callarg_plan_off_reverts_to_leak", func(t *testing.T) {
		src := optstrCallargCases()[0].src
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1", "FERN_SELFHOST_RC_PLAN=0"})
		progBin := buildBin(t, gcc, dir, "optstrcallarg_planoff", asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != 14 {
			t.Fatalf("plan-off exited %d, want 14 — the fallback must change counts, never answers", exit)
		}
		summary := leakSummaryLine(stderr)
		var allocs, frees, live int64
		if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
			t.Fatalf("parse %q: %v", summary, err)
		}
		if live == 0 {
			t.Fatalf("plan-off unexpectedly clean (%s) — the off-plan gate widened; that belongs to its own change", summary)
		}
	})
}
