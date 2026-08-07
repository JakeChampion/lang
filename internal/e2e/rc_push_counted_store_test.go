// #4399 sink 1 — Array_push is a COUNTED store, so its taint arm weakened
// to escapeOwned: emitArrayPush already inc's an aliased pointer-shaped
// element (the same Ident / FieldAccess / Index shapes the old full-escape
// taint walked), and the buffer's deep drop decs elements. That makes a
// PROJECTION source (`out.push(src[i])`) co-owned by the buffer, so the
// source container does not escape-taint and reclaims at scope exit. Without
// it `src`'s whole buffer (and transitively its elements' buffers) leaks per
// call, the dominant safe-leak class for push-heavy code
// (docs/OWNERSHIP-INFERENCE-PLAN.md).
//
// Three contracts, mirrored on x86-64 and wasm:
//   - BOUNDED: a loop over a function that pushes a projection must show
//     the same heap-bump growth at N=50 and N=5000 (the source reclaims);
//     pre-change this grew with N.
//   - CORRECT: reading the pushed element back after the source container
//     is reclaimable yields the right values (the element inc kept it
//     alive — no use-after-free).
//   - BALANCED: __rc_underflow_count() stays 0 (the new reclaim never
//     over-releases the co-owned element).
package e2e

import "testing"

func pushProjectionSrc(n string) string {
	return `function work(k: i32): i32 {
    var src: i32[][] = [[k, k + 1], [k + 2]];
    var out: i32[][] = [];
    out = out.append(src[0]);
    var e: i32[] = out[0];
    return e[0] + e[1];
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var s: i32 = 0;
    while (i < ` + n + `) {
        s = s + work(i);
        i = i + 1;
    }
    if (s == 0) { return 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestX86_64ArrayPushProjectionSourceReclaims(t *testing.T) {
	small := mustRunX86_64FreeOn(t, pushProjectionSrc("50"))
	large := mustRunX86_64FreeOn(t, pushProjectionSrc("5000"))
	// Bounded modulo freelist warm-up jitter: 4950 extra iterations must
	// not add even one page. Pre-migration the tainted source leaked its
	// buffers per call and this grew with N.
	if large > small+4096 {
		t.Errorf("push-projection bump growth should be bounded (source reclaims): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestWasmArrayPushProjectionSourceReclaims(t *testing.T) {
	small := runWasm(t, pushProjectionSrc("50"))
	large := runWasm(t, pushProjectionSrc("5000"))
	// wasm carries a pre-existing ~64 B/iter residual in this shape that
	// is NOT the push sink (it predates the migration at ~128 B/iter and
	// halved when the source became reclaimable). Ratchet: per-iteration
	// growth must stay strictly below 100 B — a regression back to the
	// tainted source (~128 B/iter) trips this, while the residual passes.
	perIter := (large - small) / 4950
	if perIter >= 100 {
		t.Errorf("push-projection per-iteration growth %dB regressed toward the tainted-source baseline (~128B): N=50 -> %d, N=5000 -> %d", perIter, small, large)
	}
}

// The pushed element must survive its source container's reclaim, and the
// element inc / container dec must balance exactly.
const pushProjectionBalanceSrc = `function work(k: i32): i32 {
    var src: string[][] = [["a", "bc"], ["def"]];
    var out: string[][] = [];
    out = out.append(src[1]);
    var e: string[] = out[0];
    return e[0].len() + k - k;
}
function main(): i32 {
    var i: i32 = 0;
    var s: i32 = 0;
    while (i < 200) {
        s = s + work(i);
        i = i + 1;
    }
    if (s != 600) { return 1; }
    return __rc_underflow_count();
}`

func TestX86_64ArrayPushProjectionNoUnderflow(t *testing.T) {
	if got := mustRunX86_64FreeOn(t, pushProjectionBalanceSrc); got != 0 {
		t.Errorf("push-projection release must stay balanced: want exit 0, got %d (non-zero = rc underflow or wrong sum)", got)
	}
}

func TestWasmArrayPushProjectionNoUnderflow(t *testing.T) {
	if got := runWasm(t, pushProjectionBalanceSrc); got != 0 {
		t.Errorf("push-projection release must stay balanced: want exit 0, got %d (non-zero = rc underflow or wrong sum)", got)
	}
}

// #4399 sink 2 — the `.with` (Array_set) analogue of the push tests:
// a pointer-shaped projection stored via .with is inc'd by emitArraySet
// (and the overwritten element dropped), so the projection's source
// container reclaims and the balance stays exact.
const withProjectionBalanceSrc = `function work(k: i32): i32 {
    var src: i32[][] = [[k, k + 1], [k + 2]];
    var out: i32[][] = [[k]];
    out = out.with(0, src[0]);
    var e: i32[] = out[0];
    return e[0] + e[1];
}
function main(): i32 {
    var i: i32 = 0;
    var s: i32 = 0;
    while (i < 200) {
        s = s + work(i);
        i = i + 1;
    }
    if (s == 0) { return 1; }
    return __rc_underflow_count();
}`

func TestX86_64ArraySetProjectionNoUnderflow(t *testing.T) {
	if got := mustRunX86_64FreeOn(t, withProjectionBalanceSrc); got != 0 {
		t.Errorf(".with-projection release must stay balanced: want exit 0, got %d", got)
	}
}

func TestWasmArraySetProjectionNoUnderflow(t *testing.T) {
	if got := runWasm(t, withProjectionBalanceSrc); got != 0 {
		t.Errorf(".with-projection release must stay balanced: want exit 0, got %d", got)
	}
}

// #4399 sink 4a — if-expr yield balance: whichever arm runs, the yielded
// alias is inc'd and every local (sources + binding) reclaims exactly once.
func ifYieldBalanceSrc(cond string) string {
	return `function pick(c: boolean, k: i32): i32 {
    var a: i32[][] = [[k, k + 1]];
    var b2: i32[][] = [[k + 2]];
    var v: i32[][] = if (c) { a } else { b2 };
    var e: i32[] = v[0];
    return e[0];
}
function main(): i32 {
    var i: i32 = 0;
    var s: i32 = 0;
    while (i < 200) {
        s = s + pick(` + cond + `, i);
        i = i + 1;
    }
    if (s == 0) { return 1; }
    return __rc_underflow_count();
}`
}

func TestX86_64IfExprYieldNoUnderflow(t *testing.T) {
	for _, cond := range []string{"true", "false", "i % 2 == 0"} {
		if got := mustRunX86_64FreeOn(t, ifYieldBalanceSrc(cond)); got != 0 {
			t.Errorf("if-yield release must stay balanced (cond=%s): want exit 0, got %d", cond, got)
		}
	}
}

func TestWasmIfExprYieldNoUnderflow(t *testing.T) {
	for _, cond := range []string{"true", "false", "i % 2 == 0"} {
		if got := runWasm(t, ifYieldBalanceSrc(cond)); got != 0 {
			t.Errorf("if-yield release must stay balanced (cond=%s): want exit 0, got %d", cond, got)
		}
	}
}

// #4399 sink 4b — match-expr yields are counted in all lowering routes.
// The general (enum) route and the literal route both yield aliased
// locals from arms; balance must stay exact whichever arm runs.
const matchYieldBalanceSrc = `enum Tag { A, B }
function pick(t: Tag, k: i32): i32 {
    var a: i32[][] = [[k, k + 1]];
    var b2: i32[][] = [[k + 2]];
    var v: i32[][] = match (t) { A => a, _ => b2 };
    var w: i32[][] = match (k % 2) { 0 => a, _ => v };
    return w[0][0];
}
function main(): i32 {
    var i: i32 = 0;
    var s: i32 = 0;
    while (i < 200) {
        var t: Tag = A;
        if (i % 3 == 0) { t = B; }
        s = s + pick(t, i);
        i = i + 1;
    }
    if (s == 0) { return 1; }
    return __rc_underflow_count();
}`

func TestX86_64MatchExprYieldNoUnderflow(t *testing.T) {
	if got := mustRunX86_64FreeOn(t, matchYieldBalanceSrc); got != 0 {
		t.Errorf("match-yield release must stay balanced: want exit 0, got %d", got)
	}
}

func TestWasmMatchExprYieldNoUnderflow(t *testing.T) {
	if got := runWasm(t, matchYieldBalanceSrc); got != 0 {
		t.Errorf("match-yield release must stay balanced: want exit 0, got %d", got)
	}
}

// #4402 opt 1 — dead-alias cancellation balance: reads through the borrowed
// view stay valid for the whole function, releases stay exact, and the churn
// loop stays flat (x reclaims once per call; the elided pair adds nothing).
const deadAliasBalanceSrc = `function work(k: i32): i32 {
    var x: i32[][] = [[k, k + 1], [k + 2]];
    var y: i32[][] = x;
    return y[1][0] + y[0][1] + x[0][0];
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var s: i32 = 0;
    while (i < 5000) {
        s = s + work(i);
        i = i + 1;
    }
    if (s == 0) { return 1; }
    if (__rc_underflow_count() != 0) { return 2; }
    if ((__heap_bump_bytes() as i32) - before > 65536) { return 3; }
    return 0;
}`

func TestX86_64DeadAliasCancellationBalanced(t *testing.T) {
	if got := mustRunX86_64FreeOn(t, deadAliasBalanceSrc); got != 0 {
		t.Errorf("dead-alias cancellation must stay balanced and flat: want exit 0, got %d", got)
	}
}

func TestWasmDeadAliasCancellationBalanced(t *testing.T) {
	if got := runWasm(t, deadAliasBalanceSrc); got != 0 {
		t.Errorf("dead-alias cancellation must stay balanced and flat: want exit 0, got %d", got)
	}
}
