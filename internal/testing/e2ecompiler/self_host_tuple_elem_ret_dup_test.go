package e2ecompiler

import (
	"strings"
	"testing"
)

// Dup-at-extract for tuple element returns: `return src.<i>` of an rc-array
// element is RETAINED on return, so the tuple it came from can still be
// released in full by its owner. The retain and the owner's release must go
// together: either alone is wrong, in opposite directions
// (docs/rc-log/2026-08-28-elemret-scoping-pin.md — the coupled
// tuple_mixed__elemret__* matrix rows are the standing instrument).
//
// Exits confirmed against `bin/fern -interp`; every early arm dynamically
// live; census + FERN_SANITIZE legs per case, and a FERN_SELFHOST_RC_PLAN=0
// leg that must change nothing.

func tupleElemRetDupCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			// The matrix cell shape in a loop: direct `return src.1` off a
			// borrowed tuple param, TUPRCS-granted caller. Element rc:
			// construction 1 -> return retain 2 -> caller's deep free 1 ->
			// final owner's sweep 0. Was one free short per round.
			name: "direct_elem_return_balances",
			src: `function get(src: (i32, i32[])): i32[] { return src.1; }
function make(i: i32): i32 {
    let keep: (i32, i32[]) = (i, [i, i + 1]);
    let r: i32[] = get(keep);
    return r.len() + keep.0;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + make(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 4,
		},
		{
			// Self-extract: the frame that owns the TUPRCS tuple also calls
			// the extractor — the retain and the frame's own deep free settle
			// in one round trip.
			name: "self_extract_balances",
			src: `function grab(i: i32): i32 {
    let t: (i32, i32[]) = (i, [i, i + 1, i + 2]);
    let r: i32[] = pick(t);
    return r.len() + t.0;
}
function pick(t: (i32, i32[])): i32[] { return t.1; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + grab(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 21,
		},
		{
			// A CONDITIONAL element return (one nested in an `if`) with a live
			// arm: the fresh-literal arm moves out, the element arm retains —
			// both paths admitted, both counted.
			name: "nested_elem_return_balances",
			src: `function get(src: (i32, i32[]), i: i32): i32[] {
    if (i % 7 == 0) { return src.1; }
    return [i];
}
function make(i: i32): i32 {
    let keep: (i32, i32[]) = (i, [i, i + 1]);
    let r: i32[] = get(keep, i);
    return r.len() + keep.0;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + make(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 2,
		},
		{
			// Adversarial: the element passed ONWARD (`sink(src.1)`) inside a
			// composite return value, not the dup shape; sink borrows it. What
			// this row pins is the EXIT and free-safety.
			name: "elem_onward_stays_sound",
			src: `function sink(xs: i32[]): i32 { return xs.len(); }
function get(src: (i32, i32[])): i32 { return sink(src.1); }
function make(i: i32): i32 {
    let keep: (i32, i32[]) = (i, [i, i + 1]);
    return get(keep) + keep.0;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + make(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 4,
		},
		{
			// Adversarial: the BIND spelling (`let e = src.1; return e`).
			// The exit and free-safety are what this row pins.
			name: "bind_spelling_stays_refused",
			src: `function get(src: (i32, i32[])): i32[] {
    let e: i32[] = src.1;
    return e;
}
function make(i: i32): i32 {
    let keep: (i32, i32[]) = (i, [i, i + 1]);
    let r: i32[] = get(keep);
    return r.len() + keep.0;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + make(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 4,
		},
	}
}

func TestSelfHostTupleElemRetDupX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleElemRetDupCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupelemret_"+tc.name, asm)
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
			sanBin := buildBin(t, gcc, dir, "tupelemret_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}

			// Plan-off leg: none of TUPB / TUPELEMOK / TUPRCS is
			// plan-routed, so FERN_SELFHOST_RC_PLAN=0 must change
			// nothing on the flipped shapes.
			offAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1", "FERN_SELFHOST_RC_PLAN=0"})
			offBin := buildBin(t, gcc, dir, "tupelemret_off_"+tc.name, offAsm)
			offErr, offExit := hevRun(t, runner, offBin)
			if offExit != tc.want {
				t.Fatalf("%s plan-off exited %d, want %d", tc.name, offExit, tc.want)
			}
			offSummary := leakSummaryLine(offErr)
			var oa, of, ol int64
			if _, err := fmtSscan(offSummary, &oa, &of, &ol); err != nil {
				t.Fatalf("%s plan-off: parse %q: %v", tc.name, offSummary, err)
			}
			if ol != 0 || oa != of {
				t.Errorf("%s plan-off: %s — the port is not plan-routed and must balance without the plan", tc.name, offSummary)
			}
		})
	}
}
