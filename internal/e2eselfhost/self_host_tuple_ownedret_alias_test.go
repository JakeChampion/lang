package e2eselfhost

import (
	"strings"
	"testing"
)

// The owned-return admission learns the #7667 alias forgiveness:
// tuple_ret_local_is_frame_fresh feeds rctuple_param_alias_bind_sites into
// the ret-forgiving escape walk, so a producer local read through a
// dead-ended alias (`var a = t; ... a.0 ...; return t;`) keeps the callee in
// tuple_fresh_ret_fns and the caller its TUP:/ARRF: credit. The vet is the
// rc-tuple payload scan on the alias's own name — an alias that is itself
// returned, chains, extracts a payload, or leaves through a call arg still
// sinks the admission, which is what keeps the caller's deep free from
// dangling a second live reference (the over-release the cell comment warns
// a careless widening buys).
//
// Every want is confirmed against BOTH oracles (bin/fern -interp and native
// x86-64) — never read off the self-host run under test. Every early arm is
// dynamically live (the 08-24 entry's trap: a dead arm makes half the shape
// unverifiable). Each case re-runs under FERN_SANITIZE=1 and must exit
// identically with no over-release / use-after-free report.

func tupleOwnedretAliasCases() []tupleAliasParamCase {
	const caller = `
function round(i: i32): i32 {
    var r: (i32, i32[]) = mk(i);
    return r.0 + r.1.len();
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`
	return []tupleAliasParamCase{
		{
			// The matrix cell tuple_mixed__ownedret_alias__bind_local, with a
			// LIVE early arm: the reader alias no longer sinks the admission,
			// so every round's box + element array frees.
			name: "alias_read_admits",
			src: `function mk(i: i32): (i32, i32[]) {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var a: (i32, i32[]) = t;
    if (a.0 % 7 == 0) { return (0, [0, 0, 0]); }
    return t;
}` + caller,
			want: 31, balance: true,
		},
		{
			// The alias RETURNED on one path: `return a` is not a bare return
			// of a frame-fresh local (its init is an ident, not a literal),
			// and the payload scan on a sees the return as an escape — the
			// admission stays refused and the floor holds. Blessing this
			// shape would hand the caller two admitted paths to one box.
			name: "alias_returned_keeps_refused",
			src: `function mk(i: i32): (i32, i32[]) {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var a: (i32, i32[]) = t;
    if (a.0 % 7 == 0) { return a; }
    return t;
}` + caller,
			want: 4,
		},
		{
			// A payload extracted through the alias: the scan on a sees the
			// non-scalar element read and refuses the site. Conservative —
			// e dies in-frame, but the admission's deep-free licence must not
			// rest on that.
			name: "alias_elem_out_keeps_refused",
			src: `function mk(i: i32): (i32, i32[]) {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var a: (i32, i32[]) = t;
    var e: i32[] = a.1;
    if ((e.len() + i) % 7 == 0) { return (0, [0, 0, 0]); }
    return t;
}` + caller,
			want: 58,
		},
		{
			// A chained alias: vetting a sees `var b = a` as a bare-ident
			// escape of a, so the site never reaches alias_ok.
			name: "alias_chained_keeps_refused",
			src: `function mk(i: i32): (i32, i32[]) {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var a: (i32, i32[]) = t;
    var b: (i32, i32[]) = a;
    if (b.0 % 7 == 0) { return (0, [0, 0, 0]); }
    return t;
}` + caller,
			want: 31,
		},
		{
			// The alias leaves through a call arg: the payload scan runs with
			// an empty registry, so any onward pass is an escape and the
			// admission stays refused (registry-independence is what keeps
			// tuple_fresh_ret_fns_of a single non-fixpoint pass).
			name: "alias_callarg_keeps_refused",
			src: `function peek(x: (i32, i32[])): i32 { return x.0; }
function mk(i: i32): (i32, i32[]) {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var a: (i32, i32[]) = t;
    if (peek(a) % 7 == 0) { return (0, [0, 0, 0]); }
    return t;
}` + caller,
			want: 31,
		},
	}
}

func TestSelfHostTupleOwnedretAliasX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleOwnedretAliasCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupownedret_"+tc.name, asm)
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

			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "tupownedret_san_"+tc.name, sanAsm)
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
