package e2e

import (
	"testing"
)

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

function noAppend(k: i32): i32[] { let xs: i32[] = [1]; return xs; }
function oneGrow(k: i32): i32[] { let xs: i32[] = [1]; xs = xs.append(k); return xs; }
function twoGrows(k: i32): i32[] {
    let xs: i32[] = [1];
    xs = xs.append(k); xs = xs.append(k); xs = xs.append(k); xs = xs.append(k); xs = xs.append(k);
    return xs;
}
function loopGrows(k: i32): i32[] {
    let xs: i32[] = [1];
    let j: i32 = 0;
    while (j < 12) { xs = xs.append(k); j = j + 1; }
    return xs;
}
// Distinct append target: the result is bound to a NEW local rather than
// rebound to the receiver, so the self-rebind carve-out does not apply. This
// is only reclaimable because the receiver has no later occurrence.
function distinctTarget(k: i32): i32[] { let xs: i32[] = [1]; let ys: i32[] = xs.append(k); return ys; }

function churnNone(n: i32): i32 { let k: i32 = n & 3; let i: i32 = 0; let a: i32 = 0; while (i < n) { let z: i32[] = noAppend(k); a = (a + z.len()) % 251; i = i + 1; } return a; }
function churnOne(n: i32): i32 { let k: i32 = n & 3; let i: i32 = 0; let a: i32 = 0; while (i < n) { let z: i32[] = oneGrow(k); a = (a + z.len()) % 251; i = i + 1; } return a; }
function churnTwo(n: i32): i32 { let k: i32 = n & 3; let i: i32 = 0; let a: i32 = 0; while (i < n) { let z: i32[] = twoGrows(k); a = (a + z.len()) % 251; i = i + 1; } return a; }
function churnLoop(n: i32): i32 { let k: i32 = n & 3; let i: i32 = 0; let a: i32 = 0; while (i < n) { let z: i32[] = loopGrows(k); a = (a + z.len()) % 251; i = i + 1; } return a; }
function churnDistinct(n: i32): i32 { let k: i32 = n & 3; let i: i32 = 0; let a: i32 = 0; while (i < n) { let z: i32[] = distinctTarget(k); a = (a + z.len()) % 251; i = i + 1; } return a; }

function main(): i32 {
    let t: i32 = 0;
    // Each phase: warm up, sample, churn 10x more, sample again. Equal
    // samples == every buffer the 10 extra calls allocated was reclaimed.
    t = t + churnNone(3); let a1: i32 = (__heap_bump_bytes() as i32); t = t + churnNone(10); let a2: i32 = (__heap_bump_bytes() as i32);
    if (a2 != a1) { return 11; }
    t = t + churnOne(3); let b1: i32 = (__heap_bump_bytes() as i32); t = t + churnOne(10); let b2: i32 = (__heap_bump_bytes() as i32);
    if (b2 != b1) { return 12; }
    t = t + churnTwo(3); let c1: i32 = (__heap_bump_bytes() as i32); t = t + churnTwo(10); let c2: i32 = (__heap_bump_bytes() as i32);
    if (c2 != c1) { return 13; }
    t = t + churnLoop(3); let d1: i32 = (__heap_bump_bytes() as i32); t = t + churnLoop(10); let d2: i32 = (__heap_bump_bytes() as i32);
    if (d2 != d1) { return 14; }
    t = t + churnDistinct(3); let e1: i32 = (__heap_bump_bytes() as i32); t = t + churnDistinct(10); let e2: i32 = (__heap_bump_bytes() as i32);
    if (e2 != e1) { return 15; }
`

const appendFreshReclaimTail = `    // Value guard: the arrays must still hold the right contents after all
    // that reclaim (a fix that frees a live buffer would corrupt these).
    let v: i32[] = loopGrows(7);
    if (v.len() != 13) { return 20; }
    if (v[0] != 1) { return 21; }
    if (v[12] != 7) { return 22; }
    let w: i32[] = distinctTarget(9);
    if (w.len() != 2) { return 24; }
    if (w[0] != 1) { return 25; }
    if (w[1] != 9) { return 26; }
    if (t < 0) { return 23; }
    return 0;
}
`

func TestInterpAppendFreshReclaim(t *testing.T) {
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
