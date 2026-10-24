package e2e

import (
	"testing"
)

const ownStructRebindSrc = `struct St { keep: i32[], drop: i32[] }
function fresh(own s: St): St { return St { keep: [7], drop: [8] }; }
function carry(own s: St): St {
    let nd: i32[] = [];
    let i: i32 = 0;
    while (i < 12) { nd = nd.append(i); i = i + 1; }
    return St { keep: s.keep, drop: nd };
}
function thread_fresh(own s: St, n: i32): St { let j: i32 = 0; while (j < n) { s = fresh(s); j = j + 1; } return s; }
function thread_carry(own s: St, n: i32): St { let j: i32 = 0; while (j < n) { s = carry(s); j = j + 1; } return s; }
function main(): i32 {
    let k: i32 = 0;
    while (k < 100) {
        let a: St = thread_fresh(St { keep: [1], drop: [] }, 4);
        if (a.keep[0] != 7) { return 80; }
        let b: St = thread_carry(St { keep: [1, 2, 3], drop: [] }, 4);
        if (b.keep[0] != 1 || b.keep.len() != 3) { return 81; }   // carried field intact
        k = k + 1;
    }
    return __rc_underflow_count();   // 0 == no double-free of the moved-out struct
}`

func TestX86_64OwnStructRebind(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, ownStructRebindSrc); code != 0 {
		t.Errorf("own-struct move-and-rebind: got %d, want 0", code)
	}
}

func TestArm64OwnStructRebind(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, ownStructRebindSrc); code != 0 {
		t.Errorf("own-struct move-and-rebind: got %d, want 0", code)
	}
}

func TestWASMOwnStructRebind(t *testing.T) {
	if got := runWasm(t, ownStructRebindSrc); got != 0 {
		t.Errorf("own-struct move-and-rebind: got %d, want 0", got)
	}
}
