package e2eselfhost

import (
	"strings"
	"testing"
)

// The "TUPB:" payload tier learns the #7553 alias forgiveness
// (rctuple_param_alias_bind_sites): a callee that binds `var x = src` and only
// READS through the alias keeps its tuple param payload-borrowable, so the
// caller's TUPRCS deep free survives. The vet is the rc-tuple payload scan on
// the alias's own name — never the box walker, which would bless the handout
// shape below (the sanitizer-confirmed UAF that killed v1 of the tier).
//
// Every want is confirmed against BOTH oracles (bin/fern -interp and native
// x86-64) — never read off the self-host run under test. Each case also
// recompiles under FERN_SANITIZE=1 and must exit identically with no
// over-release or use-after-free report (a leak report on a refused row is
// the census's business): a wrongly-blessed handout trips the quarantine,
// not the census.

type tupleAliasParamCase struct {
	name    string
	src     string
	want    int
	balance bool // flipped rows: allocs == frees at live_bytes 0
	// refused rows pin their leaking frees so a silent widening (or a
	// silent regression of the flip) moves a number, not just a verdict.
	wantFrees int64 // asserted only when balance is false
}

const tupleAliasParamMain = `
function main(): i32 {
    var keep: (i32, i32[]) = (5, [6, 7]);
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(keep, i); i = i + 1; }
    acc = (acc + keep.0 + keep.1.len()) % 83;
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

func tupleAliasParamCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			// The matrix cell tuple_mixed__fnscope__alias_param: a read-only
			// alias in the callee no longer costs main's keep its deep free.
			// Was a constant 2 allocs / 0 frees before the alias sites fed
			// the TUPB vet.
			name: "fnscope_alias_reads_only",
			src: `function round(src: (i32, i32[]), i: i32): i32 {
    var t: i32 = 0;
    var x: (i32, i32[]) = src;
    t = (t + x.0 + x.1.len()) % 101;
    return t;
}` + tupleAliasParamMain,
			want: 43, balance: true,
		},
		{
			// tuple_mixed__if_block__alias_param: the same alias inside a
			// branch — rctuple_param_alias_bind_sites recurses, so a
			// block-scoped bind is forgiven exactly like the fnscope one.
			name: "if_block_alias_reads_only",
			src: `function round(src: (i32, i32[]), i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) {
        var x: (i32, i32[]) = src;
        t = (t + x.0 + x.1.len()) % 101;
        t = t + 1;
    }
    return t;
}` + tupleAliasParamMain,
			want: 75, balance: true,
		},
		{
			// The v1-killer must stay refused: the alias hands the rc element
			// out, so blessing this site would have the caller's deep free
			// dangle every `out` the loop holds. The payload scan on x sees
			// `return x.1` and refuses the site; keep's sweep stays denied
			// and the cell stays a safe constant leak (keep's 2 allocs).
			name: "handout_elem_keeps_refused",
			src: `function get(src: (i32, i32[]), i: i32): i32[] {
    var x: (i32, i32[]) = src;
    return x.1;
}
function main(): i32 {
    var keep: (i32, i32[]) = (5, [6, 7]);
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var out: i32[] = get(keep, i);
        var churn: i32[] = [i, i + 1, i + 2];
        acc = (acc + out.len() + churn.len()) % 101;
        i = i + 1;
    }
    acc = (acc + keep.0 + keep.1.len()) % 83;
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 20, wantFrees: 101,
		},
		{
			// A chained alias refuses: vetting x scans `var y = x` as a
			// bare-ident escape of x, so the site never reaches alias_ok.
			// Conservative — keep stays a constant leak.
			name: "chained_alias_keeps_refused",
			src: `function round(src: (i32, i32[]), i: i32): i32 {
    var x: (i32, i32[]) = src;
    var y: (i32, i32[]) = x;
    return (y.0 + y.1.len()) % 101;
}` + tupleAliasParamMain,
			want: 43,
		},
		{
			// A reassigned alias: the payload scan on x sees only reads of
			// src's payloads plus a fresh-tuple rebind, and the rebind ends
			// the aliasing, so the flag may stay 1 (same box-level reasoning
			// as param_alias_bind_sites). Whatever the callee leaks of its
			// own fresh tuple is its business — the pinned exit and the
			// sanitize leg hold either way.
			name: "reassigned_alias_sound",
			src: `function round(src: (i32, i32[]), i: i32): i32 {
    var x: (i32, i32[]) = src;
    var t: i32 = (x.0 + x.1.len()) % 101;
    x = (i, [i, i + 1, i + 2]);
    t = (t + x.1.len()) % 101;
    return t;
}` + tupleAliasParamMain,
			want: 11, wantFrees: 2,
		},
	}
}

func TestSelfHostTupleAliasParamX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleAliasParamCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupaliasparam_"+tc.name, asm)
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
			if tc.balance {
				if live != 0 || allocs != frees {
					t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
				}
			} else if frees != tc.wantFrees {
				t.Errorf("%s: %s — refused row's frees moved (want %d): a widening or a flip that belongs to its own change", tc.name, summary, tc.wantFrees)
			}

			// Sanitize leg: same program, all three detectors on. Must exit
			// identically and print no sanitizer report — a wrongly-blessed
			// alias shows up here as a quarantine hit, not in the census.
			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "tupaliasparam_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			// A leak report is fine on a refused row — the census already
			// pins it. Only the free-safety detectors are fatal here.
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}
}
