package e2eselfhost

import "testing"

// A `?` leaves the function on its Err arm through the same owned-local dec
// sweep an explicit `return` runs, and that sweep must release a local whose
// alias sits textually AFTER the `?` — the scan that claims such an alias as
// a move stops at the first statement that can leave the function, and the
// `?` is one (#8442). The analysis verdict itself is pinned against native by
// TestSelfHostRcPlanDiff's move-after-try-op case; this is the runtime half,
// which stays clean whichever table the emitter consults for the elision
// (today it reads the slots whose retain it actually dropped, not the
// verdict, so the wrong verdict was inert here).
//
// The program drives the Err path (c == 0) so the leak would be on the path
// taken; the Ok path is clean either way. aliased(0) is Err(7), so 50 rounds
// leave main returning 350 - 350 = 0.
const moveAfterTryOpSrc = `
@noinline
function g(c: i32): Result[i32, i32] {
    if (c == 0) { return Err(7); }
    return Ok(c * 2);
}

@noinline
function aliased(c: i32): Result[i32, i32] {
    var x: i32[] = [1, 2, 3];
    var r: i32 = g(c)?;
    var y: i32[] = x;
    return Ok(y[0] + r);
}

function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        match (aliased(0)) {
            Ok(v) => { acc = acc + 1000; },
            Err(e) => { acc = acc + e; }
        }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return acc - 350;
}`

func TestSelfHostMoveAfterTryOpX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, moveAfterTryOpSrc, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "moveaftertryop", asm)
	stderr, exit := hevRun(t, runner, progBin)
	if exit != 0 {
		t.Fatalf("exit %d, want 0 (99 = rc underflow; anything else = a wrong answer)", exit)
	}
	summary := leakSummaryLine(stderr)
	if summary == "" {
		t.Fatal("no leakcheck summary")
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("parse %q: %v", summary, err)
	}
	if allocs == 0 {
		t.Fatal("allocated nothing — the probe is not exercising the path")
	}
	if live != 0 || allocs != frees {
		t.Errorf("%s — the alias after the `?` was claimed as a move, so the Err path's "+
			"sweep skipped the local it should have released", summary)
	}
}
