package e2e

import (
	"fmt"
	"testing"
)

// Writer.write on wasm used to bump-allocate a 12-byte iovec scratch on
// every call and never give it back, so a write loop grew the heap by 16
// bytes per write with nothing to reclaim (#8705). The scratch is a static
// slot now (writerScratchAddr), so the census must not move with the number
// of writes: what a program keeps live after N writes is what it keeps after
// 10N.
//
// The argument is a runtime concat so it is a heap-form string: an
// inline-form argument still spills through a bump block in
// emitStrNormalize, which is #8408 and not pinned here.
func wasmWriterWriteSrc(writes int) string {
	return fmt.Sprintf(`function main(): i32 {
    var w = stdout();
    var s: string = "0123456789" + "abcdefghij";
    var i: i32 = 0;
    while (i < %d) {
        match (w.write(s)) {
            Some(e) => { return 1; },
            None => { }
        }
        i = i + 1;
    }
    return 0;
}`, writes)
}

func TestWASMWriterWriteScratchIsNotPerCall(t *testing.T) {
	const n = 20
	var counts [2][3]int64
	for i, writes := range []int{n, 10 * n} {
		_, stderr, exit := runLeakCheckWasm(t, wasmWriterWriteSrc(writes), false)
		if exit != 0 {
			t.Fatalf("%d writes: exit %d, want 0; stderr: %s", writes, exit, stderr)
		}
		a, f, live := parseLeakCheckLine(t, stderr)
		counts[i] = [3]int64{a, f, live}
		t.Logf("%d writes: allocs=%d frees=%d live_bytes=%d", writes, a, f, live)
	}
	if counts[0][2] != counts[1][2] {
		t.Errorf("live bytes move with the write count (%d after %d writes, %d after %d): Writer.write is allocating per call again",
			counts[0][2], n, counts[1][2], 10*n)
	}
	if counts[0][0]-counts[0][1] != counts[1][0]-counts[1][1] {
		t.Errorf("unfreed blocks move with the write count (%d after %d writes, %d after %d)",
			counts[0][0]-counts[0][1], n, counts[1][0]-counts[1][1], 10*n)
	}
}
