package e2e

import "testing"

// TestArgsArrayRcHeader pins the `args()` container's refcount header on
// every backend (#7969).
//
// The array a runtime helper hands back to Fern code carries cap / rc / len
// at data-12 / -8 / -4. wasmbin's __fern_args built it with only the 4-byte
// length prefix, so the caller's scope-exit dec read the rc word out of the
// PREVIOUS allocation's tail: `for a in args()` in a driver large enough to
// leave a zero there reported one over-release, while the x86-64 build of the
// same source was clean.
//
// The header alone is not enough, though: `__fern_args` caches the array and
// returns the same pointer to every caller, so no one reference can be the
// last and an OWNED header (rc = 1) means the first loop that ends frees the
// array out from under the cache. The natives wrote rc = 1 and did exactly
// that — the second walk below read argv string pointers out of a recycled
// block and segfaulted. The rc word is therefore the static sentinel on all
// three backends.
//
// Written to hold at any argc, so it does not depend on how a given runner
// invokes the guest: with no arguments the two walks both see 0 elements, but
// the cached block is still 16 bytes of header that a free would hand back to
// the churn loop, and the length read back at data-4 would no longer be 0.
//
// What each leg proves: all three flip on the rc WORD — build args() with
// rc = 1 instead of the sentinel and every one of them fails. None of them
// flips on the missing header, which was wasmbin's alone and needs a driver's
// allocation traffic to leave a zero in the word before the array — the reason
// it never reduced to a small program. TestArrayReturningHelpersWriteRcHeader
// in internal/codegen/wasmbin pins that half where it is decidable: on the
// emitted bytes.
const argsRcHeaderProgram = `function walk(): i32 {
    var n: i32 = 0;
    for a in args() { n = n + a.len(); }
    return n;
}

function main(): i32 {
    var first: i32 = walk();
    // Allocate hard between the walks: a freed cache block gets handed
    // straight back out here.
    var churn: i32 = 0;
    var i: i32 = 0;
    while (i < 64) {
        var buf: i32[] = [i, i + 1, i + 2, i + 3, i + 4, i + 5, i + 6, i + 7];
        churn = churn + buf[0];
        i = i + 1;
    }
    var second: i32 = walk();
    if (first != second) { return 2; }
    if (args().len() != first_len()) { return 3; }
    return __rc_underflow_count();
}

function first_len(): i32 {
    return args().len();
}`

func TestArgsArrayRcHeader(t *testing.T) {
	t.Run("wasm32-wasi", func(t *testing.T) {
		if code := compileAndRunWasmbinMain(t, argsRcHeaderProgram); code != 0 {
			t.Errorf("exit %d, want 0: the args() array's rc header is wrong "+
				"(1 = over-release counted, 2/3 = the cached array was freed and recycled)", code)
		}
	})
	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64(t, argsRcHeaderProgram); code != 0 {
			t.Errorf("exit %d, want 0: the args() array's rc header is wrong "+
				"(1 = over-release counted, 2/3 = the cached array was freed and recycled)", code)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		if _, code := compileAndRunArm64(t, argsRcHeaderProgram); code != 0 {
			t.Errorf("exit %d, want 0: the args() array's rc header is wrong "+
				"(1 = over-release counted, 2/3 = the cached array was freed and recycled)", code)
		}
	})
}
