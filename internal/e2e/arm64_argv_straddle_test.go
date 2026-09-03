package e2e

import "testing"

// The argv strings __fern_args builds are the #6554 producer that recorded a
// string's LENGTH where __fern_alloc_rc1 had recorded length + 1 (the trailing
// NUL) — the payload size __fern_str_dec frees with. The lengths below are the
// ones where the two round to different 16-byte classes (len ≡ 8 mod 16), so
// a free at `len` would push each block onto the list one class below the one
// it was allocated from.
//
// What this pins today is the argv strings' whole life on arm64 under the
// freelist: bound, compared, concatenated and re-bound across rounds with
// every small class populated between them, with the answer and the
// over-release counter folded into the exit code, and a leak census that
// counts exactly the `args()` cache and its strings as never freed — they are
// immortal by design (the cache hands the same array to every caller), so a
// free of one would be a use-after-free through the cache, and this is where
// it would first read as `frees > 0`.
//
// The size-word contract itself is pinned structurally in
// internal/codegen/arm64 (TestTwoWordStringProducersLeaveAllocRc1SizeWord),
// because no reclaim path reaches an argv string at rc==1 while the cache
// holds it.

const argvStraddleA = "aaaaaaaa"                                 // 8
const argvStraddleB = "bbbbbbbbbbbbbbbbbbbbbbbb"                 // 24
const argvStraddleC = "cccccccccccccccccccccccccccccccccccccccc" // 40

const argvStraddleSrc = `
// Leave one block on every small freelist class (16..2048) so a block freed
// into the wrong class has a neighbour to be confused with.
function churn(): i32 {
    var held: usize[] = [];
    var n: i32 = 16;
    while (n <= 2048) {
        held = held.append(__alloc(n));
        n = n + 16;
    }
    var i: i32 = 0;
    n = 16;
    while (n <= 2048) {
        __free(held[i], n);
        i = i + 1;
        n = n + 16;
    }
    return i;
}
function main(): i32 {
    var xs: string[] = args();
    if (xs.len() != 4) { return 90; }
    var r: i32 = 0;
    while (r < 40) {
        if (churn() != 128) { return 91; }
        var a: string = xs[1];
        var b: string = xs[2];
        var c: string = xs[3];
        if (a.len() != 8) { return 92; }
        if (b.len() != 24) { return 93; }
        if (c.len() != 40) { return 94; }
        if (a != "` + argvStraddleA + `") { return 95; }
        if (b != "` + argvStraddleB + `") { return 96; }
        if (c != "` + argvStraddleC + `") { return 97; }
        var ab: string = a + b;
        var bc: string = b + c;
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
	allocs, frees, _ := parseLeakCheckLine(t, stderr)
	// The args() cache array and its four strings (argv[0] and the three
	// above) are the only blocks that must survive; everything the rounds
	// allocate comes back.
	if allocs-frees != 5 {
		t.Errorf("leakcheck: allocs-frees = %d (allocs=%d frees=%d), want 5 — the immortal args() cache and its 4 strings; "+
			"fewer means an argv string was freed under the cache, more means a round leaked", allocs-frees, allocs, frees)
	}
}
