package e2e

import (
	"testing"
)

// A local array rebuilt by `xs = xs.append(v)` and returned orphaned one buffer
// per copy-grow in the CALLER (#5608). The cause was in the freshness oracle,
// not the drop itself: computeFreshLocals' COW self-reassign carve-out listed
// `m = m.insert(..)` / `m = m.cleared()` / `a = a.with(..)` but not
// `a = a.append(..)`, so the append rebind ended the local's freshness. That
// made findReturnsNoParamEscape reject the builder, which tainted the caller's
// binding out of freeEligible, so the caller's scope-exit drop fell back to the
// non-freeing flat `__fern_rc_dec` instead of the buffer-freeing
// `__fern_arr_dec`. `append` has the same COW contract as `.with` (the same
// buffer at rc==1, a fresh copy otherwise), so a buffer that started fresh
// stays param-free across the rebind.
//
// The probe is `__heap_bump_bytes()` — the bump high-water. A bounded shape
// reports the same value after 3 iterations and after 10 more; the leak scaled
// with the number of copy-grows (1 grow → 32 B/call, 2 → 64, 3 → 112).
//
// The callees take a parameter on purpose: with a constant argument the IR
// Inline pass inlines them and the leak disappears, which is exactly what hid
// this for so long. Keep them non-inlinable.
//
// `noAppend` is the control that must stay at 0 both before and after — it
// guards against a "fix" that merely frees everything.
//
// `distinctTarget` covers the second half of #5608: the append result bound to
// a NEW local instead of rebound to the receiver. That one is reclaimable only
// because the receiver has no later occurrence — the rc==1 grow path mutates
// the receiver's buffer in place, so a later read would see the longer array.
// appendFreshReclaimSrc builds the program. `underflowGuard` adds the
// over-release assertion, which only the compiled backends can answer — the
// interpreter has no `__rc_underflow_count` (nor a bump-allocator model), so it
// runs the same source as a pure value oracle rather than SKIPping.
func appendFreshReclaimSrc(underflowGuard bool) string {
	guard := ""
	if underflowGuard {
		guard = "    if (__rc_underflow_count() != 0) { return 99; }\n"
	}
	return appendFreshReclaimHead + guard + appendFreshReclaimTail
}

const appendFreshReclaimHead = `
import "std/i32";
import "std/array";

function noAppend(k: i32): i32[] { var xs: i32[] = [1]; return xs; }
function oneGrow(k: i32): i32[] { var xs: i32[] = [1]; xs = xs.append(k); return xs; }
function twoGrows(k: i32): i32[] {
    var xs: i32[] = [1];
    xs = xs.append(k); xs = xs.append(k); xs = xs.append(k); xs = xs.append(k); xs = xs.append(k);
    return xs;
}
function loopGrows(k: i32): i32[] {
    var xs: i32[] = [1];
    var j: i32 = 0;
    while (j < 12) { xs = xs.append(k); j = j + 1; }
    return xs;
}
// Distinct append target: the result is bound to a NEW local rather than
// rebound to the receiver, so the self-rebind carve-out does not apply. This
// is only reclaimable because the receiver has no later occurrence.
function distinctTarget(k: i32): i32[] { var xs: i32[] = [1]; var ys: i32[] = xs.append(k); return ys; }

function churnNone(n: i32): i32 { var k: i32 = n & 3; var i: i32 = 0; var a: i32 = 0; while (i < n) { var z: i32[] = noAppend(k); a = (a + z.len()) % 251; i = i + 1; } return a; }
function churnOne(n: i32): i32 { var k: i32 = n & 3; var i: i32 = 0; var a: i32 = 0; while (i < n) { var z: i32[] = oneGrow(k); a = (a + z.len()) % 251; i = i + 1; } return a; }
function churnTwo(n: i32): i32 { var k: i32 = n & 3; var i: i32 = 0; var a: i32 = 0; while (i < n) { var z: i32[] = twoGrows(k); a = (a + z.len()) % 251; i = i + 1; } return a; }
function churnLoop(n: i32): i32 { var k: i32 = n & 3; var i: i32 = 0; var a: i32 = 0; while (i < n) { var z: i32[] = loopGrows(k); a = (a + z.len()) % 251; i = i + 1; } return a; }
function churnDistinct(n: i32): i32 { var k: i32 = n & 3; var i: i32 = 0; var a: i32 = 0; while (i < n) { var z: i32[] = distinctTarget(k); a = (a + z.len()) % 251; i = i + 1; } return a; }

function main(): i32 {
    var t: i32 = 0;
    // Each phase: warm up, sample, churn 10x more, sample again. Equal
    // samples == every buffer the 10 extra calls allocated was reclaimed.
    t = t + churnNone(3); var a1: i32 = __heap_bump_bytes(); t = t + churnNone(10); var a2: i32 = __heap_bump_bytes();
    if (a2 != a1) { return 11; }
    t = t + churnOne(3); var b1: i32 = __heap_bump_bytes(); t = t + churnOne(10); var b2: i32 = __heap_bump_bytes();
    if (b2 != b1) { return 12; }
    t = t + churnTwo(3); var c1: i32 = __heap_bump_bytes(); t = t + churnTwo(10); var c2: i32 = __heap_bump_bytes();
    if (c2 != c1) { return 13; }
    t = t + churnLoop(3); var d1: i32 = __heap_bump_bytes(); t = t + churnLoop(10); var d2: i32 = __heap_bump_bytes();
    if (d2 != d1) { return 14; }
    t = t + churnDistinct(3); var e1: i32 = __heap_bump_bytes(); t = t + churnDistinct(10); var e2: i32 = __heap_bump_bytes();
    if (e2 != e1) { return 15; }
`

// Over-release guard is spliced in here for the compiled backends.
const appendFreshReclaimTail = `    // Value guard: the arrays must still hold the right contents after all
    // that reclaim (a fix that frees a live buffer would corrupt these).
    var v: i32[] = loopGrows(7);
    if (v.len() != 13) { return 20; }
    if (v[0] != 1) { return 21; }
    if (v[12] != 7) { return 22; }
    var w: i32[] = distinctTarget(9);
    if (w.len() != 2) { return 24; }
    if (w[0] != 1) { return 25; }
    if (w[1] != 9) { return 26; }
    if (t < 0) { return 23; }
    return 0;
}
`

func TestInterpAppendFreshReclaim(t *testing.T) {
	// The interpreter has no bump-allocator model (__heap_bump_bytes is always
	// 0), so the high-water checks pass trivially there — it runs as the value
	// / underflow oracle for the same source.
	if code := runInterpByte(t, appendFreshReclaimSrc(false)); code != 0 {
		t.Errorf("interp append-fresh reclaim: exit = %d, want 0", code)
	}
}

func TestX86_64AppendFreshReclaim(t *testing.T) {
	if _, code := compileAndRunX86_64(t, appendFreshReclaimSrc(true)); code != 0 {
		t.Errorf("x86-64 append-fresh reclaim: exit = %d, want 0", code)
	}
}

func TestArm64AppendFreshReclaim(t *testing.T) {
	if _, code := compileAndRunArm64(t, appendFreshReclaimSrc(true)); code != 0 {
		t.Errorf("arm64 append-fresh reclaim: exit = %d, want 0", code)
	}
}

func TestWASMAppendFreshReclaim(t *testing.T) {
	if code := runWasm(t, appendFreshReclaimSrc(true)); code != 0 {
		t.Errorf("wasm append-fresh reclaim: exit = %d, want 0", code)
	}
}
