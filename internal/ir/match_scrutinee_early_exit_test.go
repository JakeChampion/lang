package ir

import "testing"

// The fresh-scrutinee box release a match owes is emitted at the post-match
// JOIN, which only the fall-through edge reaches. An arm ending in a
// `return` / `break` / `continue` branches straight past it, so the box
// leaked once per round — 32 B for a heap-boxed Result, 16 B for the
// `m.get(k)` rebox, unbounded (#6417).
//
// reclaimableMatchScrutinee's verdict is identical in both programs below —
// it never inspects an arm body — so the difference is pure reachability,
// and the assertion is on the NUMBER of releases emitted, one per exit that
// leaves the match.

const matchEarlyExitSrc = `function make(i: i64): Result[i64, i64] {
    if (i % 2i64 == 0i64) { return Ok(i); }
    return Err(i);
}
function fallThrough(i: i64): i32 {
    var acc: i32 = 0;
    match (make(i)) { Ok(v) => { acc = (v as i32) + 1; }, Err(_) => { acc = 0; } }
    return acc;
}
function bothArmsReturn(i: i64): i32 {
    match (make(i)) { Ok(v) => { return (v as i32) + 1; }, Err(_) => { return 0; } }
}
function oneArmReturns(i: i64): i32 {
    var acc: i32 = 0;
    match (make(i)) { Ok(v) => { return (v as i32) + 1; }, Err(_) => { acc = 0; } }
    return acc;
}
function armContinues(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        match (make(i as i64)) { Ok(v) => { acc = acc + (v as i32); }, Err(_) => { i = i + 1; continue; } }
        i = i + 1;
    }
    return acc;
}
function armContinuesLabeled(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    outer: while (i < n) {
        var j: i32 = 0;
        while (j < 2) {
            match (make(i as i64)) { Ok(v) => { acc = acc + (v as i32); }, Err(_) => { i = i + 1; continue outer; } }
            j = j + 1;
        }
        i = i + 1;
    }
    return acc;
}
function matchWrapsLoop(n: i32): i32 {
    var acc: i32 = 0;
    match (make(0i64)) {
        Ok(v) => {
            var i: i32 = 0;
            while (i < n) { if (i > 3) { break; } acc = acc + (v as i32); i = i + 1; }
        },
        Err(_) => { acc = 0; }
    }
    return acc;
}
function exprArmReturns(i: i64): i32 {
    var r: i32 = match (make(i)) { Ok(v) => { return (v as i32) + 1; }, Err(_) => 0 };
    return r;
}
function main(): i32 { return 0; }`

// dropCount counts the enum-box releases a function emits. The shape is
// is_unique-gated, and the gate call is one per release site.
func enumBoxReleaseCount(fn *Func) int {
	return countCallDirect(fn.Ops, "__fern_rc_is_unique")
}

func TestMatchScrutineeReleasedAtEveryExit(t *testing.T) {
	// The join release is emitted unconditionally, so the count is 1 plus
	// one per early exit sitting inside the match. On a match every arm
	// leaves, the join copy is emitted into unreachable code.
	cases := []struct {
		fn   string
		want int
	}{
		{"fallThrough", 1},
		{"bothArmsReturn", 3},
		{"oneArmReturns", 2},
		{"armContinues", 2},
		// A LABELED continue leaves two loops, and the match sits inside
		// both, so it owes the release there as well.
		{"armContinuesLabeled", 2},
		// The mirror bound: the match WRAPS the loop, so a `break` inside it
		// stays inside the match and must not release the box early.
		{"matchWrapsLoop", 1},
		{"exprArmReturns", 2},
	}
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, matchEarlyExitSrc, ptrW)
		for _, c := range cases {
			fn := findFunc(p, c.fn)
			if fn == nil {
				t.Fatalf("no function %q", c.fn)
			}
			if n := enumBoxReleaseCount(fn); n != c.want {
				t.Errorf("ptrW=%d: %s emitted %d scrutinee-box releases, want %d — "+
					"an exit that leaves the match without one leaks the box; ops:\n%s",
					ptrW, c.fn, n, c.want, p)
			}
		}
	}
}
