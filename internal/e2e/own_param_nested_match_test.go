package e2e

import "testing"

// A consuming match over one `own` parameter nested in an arm of another's
// (#10944). The inner match frees its box on the paths that reach it, but the
// moved-locals claim only sees matches every path evaluates, so the exit sweep
// deep-dropped the inner parameter a second time: the native binary hung or
// crashed by the seventh call. Every path here frees each box and array once:
// the inner payload arm, its payloadless arm, a guard that holds and one that
// fails, and the outer arm that never reaches the inner match.
const ownParamNestedMatchSrc = `enum Box { Arr(i32[]), Nil }
enum Other { Thing(i32[]), Nothing }

@noinline function two(own b: Box, own o: Other): i32 {
    match (b) { Arr(a) => { let n = a.len(); match (o) { Thing(c) => { return n + c.len(); }, Nothing => { return n; } } }, Nil => { return 0; } }
    return 0;
}

@noinline function guarded(own b: Box, own o: Other): i32 {
    match (b) {
        Arr(a) => {
            let n = a.len();
            match (o) { Thing(c) when c.len() > 3 => { return n * 10; }, Thing(c) => { return n + c.len(); }, Nothing => { return n; } }
        },
        Nil => { return 1; }
    }
    return 0;
}

function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 10) {
        // Dynamic payloads keep this a heap-ownership probe after constant
        // arrays and their enum boxes can be emitted as static data.
        t = t + two(Arr([i, 2, 3]), Thing([i, 5, 6, 7])) + two(Arr([i]), Nothing) + two(Nil, Thing([i]));
        t = t + guarded(Arr([i, 2]), Thing([i, 5, 6, 7])) + guarded(Arr([i, 2]), Thing([i])) + guarded(Nil, Nothing);
        i = i + 1;
    }
    return t - 278;
}
`

func TestOwnParamNestedMatchX86_64Sanitized(t *testing.T) {
	checkSanitizedBalanced(t, ownParamNestedMatchSrc, 42, runSanitizeX86_64)
}

func TestOwnParamNestedMatchArm64Sanitized(t *testing.T) {
	checkSanitizedBalanced(t, ownParamNestedMatchSrc, 42, runSanitizeArm64)
}

func TestOwnParamNestedMatchWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, ownParamNestedMatchSrc); got != 42 {
		t.Fatalf("wasm exited %d, want 42", got)
	}
}
