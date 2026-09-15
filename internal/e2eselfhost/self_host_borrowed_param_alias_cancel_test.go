package e2eselfhost

import (
	"strings"
	"testing"
)

// The RUNTIME half of the borrowed-parameter leg (#9291, landed by #9298):
// `var v = p` where p is an array parameter this frame only borrows takes no
// reference of its own, because the caller owns p across the whole call and
// the callee never releases it.
//
// TestSelfHostRcPlanDiff pins the PLAN and TestSelfHostLeakMatrixX86_64 pins
// a per-round census verdict. Neither can see what actually breaks if the
// cancellation half-lands: the pair is NET-ZERO, so a missing retain with the
// sweep dec still in place, or a `.with` mutating the caller's array through
// the alias, moves the ANSWER and leaves allocs == frees. These cases check
// the answer alongside the balance — the refused rows assert the caller's
// buffer is untouched and that the copy carries the new element.
//
// Exits confirmed against both oracles (bin/fern -interp and native x86-64);
// each case re-runs under FERN_SANITIZE=1.
func borrowedParamAliasCancelCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			// The cancelled shape: every mention of v is a read through the
			// value, so the confinement gate admits it and no retain is
			// emitted. xs is read after v, on the same borrowed reference.
			name: "read_through_alias",
			src: `function g(xs: i32[]): i32 {
    var v: i32[] = xs;
    return v[0] + v[1] + xs[2] + v.len();
}
function round(i: i32): i32 {
    var buf: i32[] = [i, i + 1, i + 2, i + 3];
    return g(buf);
}
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 87, balance: true,
		},
		{
			// REFUSED: the alias is a `.with` receiver. Cancelling the retain
			// here would leave __fern_arr_cow_inplace seeing rc == 1 and
			// mutating the CALLER's buffer, so the leg declines and the
			// retain stands. The round asserts both halves of that — the copy
			// carries the new element (99 - i) and buf[0] is untouched — so a
			// widening that admitted this shape reads 1000 per round, not a
			// census move.
			name: "with_receiver_refused",
			src: `function g(xs: i32[]): i32 {
    var v: i32[] = xs;
    var w: i32[] = v.with(0, 99);
    return w[0] - v[0];
}
function round(i: i32): i32 {
    var buf: i32[] = [i, i + 1, i + 2, i + 3];
    var d: i32 = g(buf);
    if (d != 99 - i) { return 1000; }
    if (buf[0] != i) { return 1000; }
    return 1;
}
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 6, balance: true,
		},
		{
			// REFUSED: the alias leaves the frame whole. The return hands out
			// the pointer itself rather than a read through it, which the
			// confinement gate refuses outright — the caller's buffer is what
			// comes back, and the retain that pays for it must stand.
			name: "returned_alias_refused",
			src: `function g(xs: i32[]): i32[] {
    var v: i32[] = xs;
    return v;
}
function round(i: i32): i32 {
    var buf: i32[] = [i, i + 1, i + 2, i + 3];
    var out: i32[] = g(buf);
    return out[1] + buf[1];
}
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 42, balance: true,
		},
	}
}

func TestSelfHostBorrowedParamAliasCancelX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range borrowedParamAliasCancelCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "bpaliascancel_"+tc.name, asm)
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
			sanBin := buildBin(t, gcc, dir, "bpaliascancel_san_"+tc.name, sanAsm)
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
