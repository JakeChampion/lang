package e2e

import "testing"

func pushProjectionSrc(n string) string {
	return `function work(k: i32): i32 {
    let src: i32[][] = [[k, k + 1], [k + 2]];
    let out: i32[][] = [];
    out = out.append(src[0]);
    let e: i32[] = out[0];
    return e[0] + e[1];
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let s: i32 = 0;
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
	if large > small+4096 {
		t.Errorf("push-projection bump growth should be bounded (source reclaims): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestWasmArrayPushProjectionSourceReclaims(t *testing.T) {
	small := runWasm(t, pushProjectionSrc("50"))
	large := runWasm(t, pushProjectionSrc("5000"))
	perIter := (large - small) / 4950
	if perIter >= 100 {
		t.Errorf("push-projection per-iteration growth %dB regressed toward the tainted-source baseline (~128B): N=50 -> %d, N=5000 -> %d", perIter, small, large)
	}
}

const pushProjectionBalanceSrc = `function work(k: i32): i32 {
    let src: string[][] = [["a", "bc"], ["def"]];
    let out: string[][] = [];
    out = out.append(src[1]);
    let e: string[] = out[0];
    return e[0].len() + k - k;
}
function main(): i32 {
    let i: i32 = 0;
    let s: i32 = 0;
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

const withProjectionBalanceSrc = `function work(k: i32): i32 {
    let src: i32[][] = [[k, k + 1], [k + 2]];
    let out: i32[][] = [[k]];
    out = out.with(0, src[0]);
    let e: i32[] = out[0];
    return e[0] + e[1];
}
function main(): i32 {
    let i: i32 = 0;
    let s: i32 = 0;
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

func ifYieldBalanceSrc(cond string) string {
	return `function pick(c: boolean, k: i32): i32 {
    let a: i32[][] = [[k, k + 1]];
    let b2: i32[][] = [[k + 2]];
    let v: i32[][] = if (c) { a } else { b2 };
    let e: i32[] = v[0];
    return e[0];
}
function main(): i32 {
    let i: i32 = 0;
    let s: i32 = 0;
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

const matchYieldBalanceSrc = `enum Tag { A, B }
function pick(t: Tag, k: i32): i32 {
    let a: i32[][] = [[k, k + 1]];
    let b2: i32[][] = [[k + 2]];
    let v: i32[][] = match (t) { A => a, _ => b2 };
    let w: i32[][] = match (k % 2) { 0 => a, _ => v };
    return w[0][0];
}
function main(): i32 {
    let i: i32 = 0;
    let s: i32 = 0;
    while (i < 200) {
        let t: Tag = A;
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

const deadAliasBalanceSrc = `function work(k: i32): i32 {
    let x: i32[][] = [[k, k + 1], [k + 2]];
    let y: i32[][] = x;
    return y[1][0] + y[0][1] + x[0][0];
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let s: i32 = 0;
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
