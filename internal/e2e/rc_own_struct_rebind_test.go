package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Move-and-rebind of an `own` STRUCT parameter — `s = f(.., s, ..)` where `f`
// takes `s` by `own` — in a loop. `s` is MOVED into `f` (which deep-drops it at
// its own exit); the assignment overwrite must NOT also deep-drop the old `s`,
// or the box is freed twice. That double-free (latent — own-struct-by-value
// threading was untested) corrupted the heap after a couple of calls and
// crashed. Fixed by `callConsumesIdent`: the overwrite skips the drop when the
// RHS consumes the target, mirroring the existing `constructionMovesIdent` skip
// for `x = Ctor(.., x, ..)`. (Own ARRAY rebind already worked via the rc-gated
// `__fern_arr_dec` overwrite — #2524/#2533; this is the struct case.)
//
// Two shapes, both pinned: a fully-dead `s` (no carry — the minimal double-free)
// and a field-carrying return (`St{ keep: s.keep, drop: fresh }` — the BState
// threading shape). The carried field is balanced by the construction alias-inc,
// so the exit deep-drop frees only the orphaned field; the carried one survives
// (verified intact) and there is no over-release (`__rc_underflow_count()==0`).
const ownStructRebindSrc = `struct St { keep: i32[], drop: i32[] }
function fresh(own s: St): St { return St { keep: [7], drop: [8] }; }
function carry(own s: St): St {
    var nd: i32[] = [];
    var i: i32 = 0;
    while (i < 12) { nd = nd.append(i); i = i + 1; }
    return St { keep: s.keep, drop: nd };
}
function thread_fresh(own s: St, n: i32): St { var j: i32 = 0; while (j < n) { s = fresh(s); j = j + 1; } return s; }
function thread_carry(own s: St, n: i32): St { var j: i32 = 0; while (j < n) { s = carry(s); j = j + 1; } return s; }
function main(): i32 {
    var k: i32 = 0;
    while (k < 100) {
        var a: St = thread_fresh(St { keep: [1], drop: [] }, 4);
        if (a.keep[0] != 7) { return 80; }
        var b: St = thread_carry(St { keep: [1, 2, 3], drop: [] }, 4);
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
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, ownStructRebindSrc); got != 0 {
		t.Errorf("own-struct move-and-rebind: got %d, want 0", got)
	}
}
