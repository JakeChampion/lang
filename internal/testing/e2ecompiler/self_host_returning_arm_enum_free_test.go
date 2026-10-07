package e2ecompiler

import (
	"strings"
	"testing"
)

// --- Consumed-match enum frees on the `return`-out-of-an-arm paths (#6219) ---
//
// A consumed enum box is released on every path out of the consuming match:
// an arm that `return`s releases it before the return, and the fallthrough
// paths release it after the match. Released only after the match, a scalar
// enum, a scalar Option and an rc-payload enum each leaked one box per call,
// unbounded (`frees=0` on every probe below). Each site zeroes the slot it
// releases and `__fern_rc_dec` is null-safe, so no path is claimed twice.
//
// The gate is an exact `allocs == frees` balance rather than `live_bytes == 0`.
// An over-release is the failure mode that matters here — the release now runs
// from two sites for one candidate — and a leak-only assertion would not see
// it. `__rc_underflow_count()` is checked inside the probes too (exit 99), so a
// double-dec that happens to rebalance the byte count still fails.
//
// Measured, self-host x86-64, allocs/frees/live_bytes before -> after #6219:
//
//	scalar-enum-returning-arms       100/0/4800    -> 100/100/0
//	scalar-enum-fallthrough-control  100/100/0     -> 100/100/0   (unchanged)
//	scalar-option-returning-arms     100/0/4000    -> 100/100/0
//	rcpayload-enum-returning-arms    200/0/8800    -> 200/200/0
//	rcpayload-moved-out-of-arm       200/100/4800  -> 200/200/0
//	mixed-return-and-fallthrough     100/51/2352   -> 100/100/0
//	loop-block-returning-arm         202/201/48    -> 202/202/0
//
// The two partial rows are the mechanism stated in numbers. `mixed` freed
// exactly the 51 rounds that fell through and leaked the 49 that returned;
// `loop-block` freed every iteration but the one that returned out of the loop.

// The issue's own reproducer. Every arm returns, so the post-match free is dead
// code on every dynamic path: 100 allocs, 0 frees, 4800 live bytes.
const raeScalarEnumSrc = `enum E { Box(i32, i32), Nil }
function round(i: i32): i32 {
    let e: E = Box(i, i);
    match (e) { Box(a, b) => { return a + b; }, Nil => { return 0; } }
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 7;
}`

// The CONTROL, and the reason the class is easy to misattribute: the identical
// program with non-returning arms reaches the post-match free and was always
// balanced. It pins that the fix did not disturb the path it sits beside.
const raeScalarEnumFallthroughSrc = `enum E { Box(i32, i32), Nil }
function round(i: i32): i32 {
    let e: E = Box(i, i);
    let t: i32 = 0;
    match (e) { Box(a, b) => { t = a + b; }, Nil => { t = 0; } }
    if (__rc_underflow_count() != 0) { return 99; }
    return t;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    return t % 7;
}`

// The scalar-OPTION half: `let o = Some(<scalar>)` is released on the same
// footing as an inline enum ctor, so a returning arm owes it the same release.
const raeScalarOptionSrc = `function round(i: i32): i32 {
    let o: Option[i32] = Some(i);
    match (o) { Some(a) => { return a; }, None => { return 0; } }
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 7;
}`

// The rc-PAYLOAD enum sibling, which leaks the same way — worse, in fact: the
// box AND its array payload, two objects per round. Its release dispatches on
// the runtime variant and must know which payload fields an arm moved out.
//
// No arm binds the payload here, so the moved set is empty and the deep-drop
// releases the array.
const raeRcPayloadEnumSrc = `enum E { Box(i32[], i32), Nil }
function round(i: i32): i32 {
    let e: E = Box([i, i + 1], i);
    match (e) { Box(_, b) => { return b; }, Nil => { return 0; } }
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 7;
}`

// The MOVED-payload case, and the one that would dangle rather than leak if the
// returning-arm release ignored the moved set. The arm binds the array payload
// and RETURNS it, so `Box#0` is moved out and the deep-drop must skip that
// field's dec while still freeing the box — the caller reads the buffer back
// afterwards, so a lost skip is a use-after-free, not a number that is merely
// off.
const raeMovedPayloadSrc = `enum E { Box(i32[], i32), Nil }
function mk(i: i32): i32[] {
    let e: E = Box([i, i + 1], i);
    match (e) { Box(a, b) => { return a; }, Nil => { return []; } }
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { let v: i32[] = mk(r); t = t + v[0] + v[1]; r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 7;
}`

// One arm both returns AND falls through, so BOTH release sites are live in the
// same function and each dynamic path must reach exactly one. This is the case
// that fails if the pending entry is emitted unconditionally rather than on the
// return edge, or if the post-match site were moved instead of kept.
const raeMixedPathsSrc = `enum E { Box(i32, i32), Nil }
function round(i: i32): i32 {
    let e: E = Box(i, i);
    let t: i32 = 0;
    match (e) { Box(a, b) => { if (a > 50) { return a + b; } t = a; }, Nil => { t = 0; } }
    return t;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 7;
}`

// The BLOCK-level mirror: the candidate is declared inside a loop body, so
// lower_block emits its free rather than the fn-body loop, and the `return`
// leaves the loop and the function at once. Both emission sites needed the same
// pending entry — fixing only the fn-level one leaves this shape leaking.
const raeLoopBlockSrc = `enum E { Box(i32, i32), Nil }
function round(n: i32): i32 {
    let r: i32 = 0;
    while (r < n) {
        let e: E = Box(r, r);
        match (e) { Box(a, b) => { if (a + b > 400) { return a; } }, Nil => {} }
        r = r + 1;
    }
    return 0;
}
function main(): i32 {
    if (__rc_underflow_count() != 0) { return 99; }
    return round(300) % 7;
}`

func TestSelfHostReturningArmEnumFreeX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	counts := func(t *testing.T, name, src string, wantExit int) (int64, int64, int64) {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit == 99 {
			t.Fatalf("%s: __rc_underflow_count() != 0 — the box was released more than once", name)
		}
		if exit != wantExit {
			t.Fatalf("%s exited %d, want %d — the release reached a live value", name, exit, wantExit)
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
		name     string
		src      string
		wantExit int
		// wantAllocs pins how many objects the shape allocates, so a probe that
		// stops exercising the class (an admission narrowing that drops the
		// candidate entirely, say) fails loudly instead of passing on a balance
		// of nothing.
		wantAllocs int64
	}{
		{"scalar-enum-returning-arms", raeScalarEnumSrc, 2, 100},
		{"scalar-enum-fallthrough-control", raeScalarEnumFallthroughSrc, 2, 100},
		{"scalar-option-returning-arms", raeScalarOptionSrc, 1, 100},
		{"rcpayload-enum-returning-arms", raeRcPayloadEnumSrc, 1, 200},
		{"rcpayload-moved-out-of-arm", raeMovedPayloadSrc, 4, 200},
		{"mixed-return-and-fallthrough", raeMixedPathsSrc, 1, 100},
		{"loop-block-returning-arm", raeLoopBlockSrc, 5, 202},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src, tc.wantExit)
			if allocs != tc.wantAllocs {
				t.Errorf("%s: allocs=%d, want %d — the probe is no longer allocating the shape it measures",
					tc.name, allocs, tc.wantAllocs)
			}
			if allocs != frees || live != 0 {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — want an exact balance. frees=0 is the "+
					"#6219 leak (the post-match release is dead code when the arm returns); frees > allocs "+
					"means the pending release and the post-match release both ran on one path",
					tc.name, allocs, frees, live)
			}
		})
	}
}
