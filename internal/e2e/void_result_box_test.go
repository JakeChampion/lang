package e2e

import (
	"fmt"
	"testing"
)

// `Result[void, IoError]`'s Ok box is built by the native runtimes at 16
// bytes with the unit at +8, and the IR frees a box at the size its layout
// says. At a 4-byte unit slot the layout said 8, so every void-result
// helper call returned a 32-byte block to the 16-byte freelist class: the
// census read allocs == frees with 16 live bytes a call (#8809). The unit
// slot is pointer-width now, so the two sizes agree.
//
// A write_file / remove_file pair is the probe: the result boxes and the
// helpers' NUL-terminated path copies (#9001) are the only allocations, so
// the census must be exact and must not move with the round count.
func voidResultLoopSrc(rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    var i: i32 = 0;
    while (i < %d) {
        match (write_file("/tmp/fern_void_result_probe.txt", "x")) { Ok(_) => { }, Err(_) => { return 1; } }
        match (remove_file("/tmp/fern_void_result_probe.txt")) { Ok(_) => { }, Err(_) => { return 2; } }
        i = i + 1;
    }
    return 0;
}`, rounds)
}

func checkVoidResultCensus(t *testing.T, target string, rounds int, stderr string, exit int) {
	t.Helper()
	if exit != 0 {
		t.Fatalf("%s %d rounds: exit %d, want 0; stderr: %s", target, rounds, exit, stderr)
	}
	a, f, live := parseLeakCheckLine(t, stderr)
	t.Logf("%s %d rounds: allocs=%d frees=%d live_bytes=%d", target, rounds, a, f, live)
	if a != f || live != 0 {
		t.Errorf("%s %d rounds: allocs=%d frees=%d live_bytes=%d, want a balanced census with 0 live: "+
			"the Ok box is freed at a different size than it was allocated, or a path copy is stranded", target, rounds, a, f, live)
	}
}

func TestX86_64VoidResultBoxIsFreedAtItsSize(t *testing.T) {
	for _, rounds := range []int{100, 200} {
		_, stderr, exit := runLeakCheckX86_64(t, voidResultLoopSrc(rounds))
		checkVoidResultCensus(t, "x86-64", rounds, stderr, exit)
	}
}

func TestArm64VoidResultBoxIsFreedAtItsSize(t *testing.T) {
	for _, rounds := range []int{100, 200} {
		_, stderr, exit := runLeakCheckArm64(t, voidResultLoopSrc(rounds))
		checkVoidResultCensus(t, "arm64", rounds, stderr, exit)
	}
}
