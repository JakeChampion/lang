package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Call-bound scalar enum locals reclaim (#6360) --------------------------
//
// `consumed_scalar_enum_frees` admitted an init only when it was a DIRECT
// constructor — `fresh_scalar_option_init` matches the callee by name (Some /
// Ok / Err / None). A `var v: Option[i32] = mk(i)` binding was therefore never
// even a candidate, so no free was emitted and the box leaked on every
// iteration: `frees=0`, one box per round, while the byte-identical shape with
// the constructor written inline was flat at 0.
//
// The freshness proof already existed and was going unused. opt_fresh_ret_fns_of
// registers every free function whose Option/Result return is always a direct
// constructor, and lower_func seeds it as "OPTFRESH:<name>" — but only
// lower_try's `?`-edge consulted it. The user-enum sibling
// (collect_fresh_rcenum_names via rcenum_call_init_owner) has admitted call
// inits since #4355 slice 5; this is the same admission for Option / Result.
//
// The gate is STRICTER than the direct sibling's, and the refused case below is
// why: the direct path knows which variant it built and checks that variant's
// payload, while a call's variant is not statically known. The free is SHALLOW,
// so a Result whose Err arm carries a string pointer would leak that payload.
// Requiring EVERY variant's payload to be scalar is what makes it sound.
//
// Both directions are pinned. Loosening the gate to admit a mixed Result makes
// the hazard case fail rather than silently leaking a payload.

const cbeOptCallSrc = `function mk(i: i32): Option[i32] {
    if (i < 0) { return None; }
    return Some(i);
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Option[i32] = mk(i);
        match (v) { Some(x) => { acc = acc + x; }, None => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

const cbeResultScalarSrc = `function mk(i: i32): Result[i32, i32] {
    if (i < 0) { return Err(0); }
    return Ok(i);
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[i32, i32] = mk(i);
        match (v) { Ok(x) => { acc = acc + x; }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// The direct-ctor form, which already reclaimed. A regression guard: the new
// call-init admission must not disturb the path it sits beside.
const cbeDirectSrc = `function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[i32, string] = Ok(i);
        match (v) { Ok(x) => { acc = acc + x; }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

// HAZARD: Err carries a string, so the variant a call returns is not statically
// known and a shallow box free would strand the string payload. Must stay
// REFUSED — i.e. must still leak. Asserting the leak is the point: it is the
// safe direction, and a change that admits this shape needs to fail here and be
// looked at rather than quietly leaking a payload.
const cbeMixedResultSrc = `function mk(i: i32): Result[i32, string] {
    if (i < 0) { return Err("neg"); }
    return Ok(i);
}
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var v: Result[i32, string] = mk(i);
        match (v) { Ok(x) => { acc = acc + x; }, Err(_) => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

func TestSelfHostCallBoundEnumReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	counts := func(t *testing.T, name, src string, wantExit int) (int64, int64, int64) {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != wantExit {
			t.Fatalf("%s exited %d, want %d", name, exit, wantExit)
		}
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("%s: no leakcheck summary", name)
		}
		var allocs, frees, live int64
		if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
			t.Fatalf("%s: parse %q: %v", name, summary, err)
		}
		if allocs == 0 {
			t.Fatalf("%s allocated nothing — the probe is not exercising the path", name)
		}
		return allocs, frees, live
	}

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{"option_scalar_from_call", cbeOptCallSrc, 72},
		{"result_all_scalar_from_call", cbeResultScalarSrc, 72},
		{"result_direct_ctor", cbeDirectSrc, 72},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src, tc.want)
			if live != 0 {
				t.Errorf("%s: live_bytes=%d, want 0 — one unfreed box per round (allocs=%d frees=%d)",
					tc.name, live, allocs, frees)
			}
		})
	}

	t.Run("mixed_result_from_call_stays_refused", func(t *testing.T) {
		allocs, frees, live := counts(t, "mixed_result_from_call", cbeMixedResultSrc, 72)
		if live <= 0 {
			t.Errorf("mixed Result now reclaims (allocs=%d frees=%d live=%d). That is only safe if the "+
				"free became variant-aware or deep — a shallow box free strands the Err arm's string "+
				"payload. Re-read fresh_scalar_option_call_init before taking this green.", allocs, frees, live)
		}
	})
}
