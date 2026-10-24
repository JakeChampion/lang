package e2e

import "testing"

const argvStraddleA = "aaaaaaaa"
const argvStraddleB = "bbbbbbbbbbbbbbbbbbbbbbbb"
const argvStraddleC = "cccccccccccccccccccccccccccccccccccccccc"

const argvStraddleSrc = `
// Leave one block on every small freelist class (16..2048) so a block freed
// into the wrong class has a neighbour to be confused with.
function churn(): i32 {
    let held: usize[] = [];
    let n: i32 = 16;
    while (n <= 2048) {
        held = held.append(__alloc(n));
        n = n + 16;
    }
    let i: i32 = 0;
    n = 16;
    while (n <= 2048) {
        __free(held[i], n);
        i = i + 1;
        n = n + 16;
    }
    return i;
}
function main(): i32 {
    let xs: string[] = args();
    if (xs.len() != 4) { return 90; }
    let r: i32 = 0;
    while (r < 40) {
        if (churn() != 128) { return 91; }
        let a: string = xs[1];
        let b: string = xs[2];
        let c: string = xs[3];
        if (a.len() != 8) { return 92; }
        if (b.len() != 24) { return 93; }
        if (c.len() != 40) { return 94; }
        if (a != "` + argvStraddleA + `") { return 95; }
        if (b != "` + argvStraddleB + `") { return 96; }
        if (c != "` + argvStraddleC + `") { return 97; }
        let ab: string = a + b;
        let bc: string = b + c;
        if (ab.len() + bc.len() != 96) { return 98; }
        r = r + 1;
    }
    return __rc_underflow_count();
}`

func TestArm64ArgvStraddleFreeOn(t *testing.T) {
	args := []string{argvStraddleA, argvStraddleB, argvStraddleC}
	if _, code := compileAndRunArm64FreeOnArgs(t, argvStraddleSrc, args...); code != 0 {
		t.Errorf("argv straddle under the freelist: exit %d, want 0 (9x = wrong answer, else an over-release)", code)
	}
	_, stderr, code := runLeakCheckArm64Args(t, argvStraddleSrc, args...)
	if code != 0 {
		t.Fatalf("leakcheck build: exit %d, want 0: %q", code, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs < 5 || allocs != frees || live != 0 {
		t.Errorf("leakcheck: allocs=%d frees=%d live_bytes=%d, want at least the args() array and its 4 boxes, all released", allocs, frees, live)
	}
}
