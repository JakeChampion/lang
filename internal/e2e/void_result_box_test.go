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
// write_file alone is the probe: on x86-64 its only allocation is the
// result box, so the census must be exact and must not move with the round
// count. (remove_file also strands its NUL-terminated path copy, which is a
// separate leak and would mask this one.)
func voidResultLoopSrc(rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    var i: i32 = 0;
    while (i < %d) {
        match (write_file("/tmp/fern_void_result_probe.txt", "x")) { Ok(_) => { }, Err(_) => { return 1; } }
        i = i + 1;
    }
    return 0;
}`, rounds)
}

func TestX86_64VoidResultBoxIsFreedAtItsSize(t *testing.T) {
	for _, rounds := range []int{100, 200} {
		_, stderr, exit := runLeakCheckX86_64(t, voidResultLoopSrc(rounds))
		if exit != 0 {
			t.Fatalf("%d rounds: exit %d, want 0; stderr: %s", rounds, exit, stderr)
		}
		a, f, live := parseLeakCheckLine(t, stderr)
		t.Logf("%d rounds: allocs=%d frees=%d live_bytes=%d", rounds, a, f, live)
		if a != f || live != 0 {
			t.Errorf("%d rounds: allocs=%d frees=%d live_bytes=%d, want a balanced census with 0 live: "+
				"the Ok box is freed at a different size than it was allocated", rounds, a, f, live)
		}
	}
}
