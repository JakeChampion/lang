package e2e

import (
	"fmt"
	"testing"
)

func wasmWriterWriteSrc(writes int) string {
	return fmt.Sprintf(`function main(): i32 {
    let w = stdout();
    let s: string = "0123456789" + "abcdefghij";
    let i: i32 = 0;
    while (i < %d) {
        match (w.write(s)) {
            Err(e) => { return 1; },
            Ok(_) => { }
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
