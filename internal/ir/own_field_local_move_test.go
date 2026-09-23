package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// #10084: `var a = s.a` and `var S { a, b } = s` move the field out of s's
// box, gated on is_unique(s) at runtime, when nothing after the declaration
// can read the field again. The shapes that could are left as retains.
const ownFieldLocalSrc = `struct S { a: i64[], b: string[], n: i32 }
function viaVar(own s: S, i: i32): S {
    var a: i64[] = s.a;
    a = a.with(i, 1 as i64);
    return S { ...s, a: a };
}
function viaPattern(own s: S, i: i32): S {
    var S { a, b, n } = s;
    a = a.with(i, 1 as i64);
    return S { a: a, b: b, n: n };
}
function rereads(own s: S, i: i32): i64 {
    var a: i64[] = s.a;
    a = a.with(i, 1 as i64);
    return s.a[i] + a[i];
}
function inLoop(own s: S, k: i32): S {
    var i: i32 = 0;
    while (i < k) {
        var a: i64[] = s.a;
        a = a.with(0, 1 as i64);
        s = S { ...s, a: a };
        i = i + 1;
    }
    return s;
}
function viaPatternKeeps(i: i32): i64 {
    var s: S = S { a: [0 as i64, 0 as i64], b: ["x"], n: 1 };
    var S { a, b, n } = s;
    a = a.with(i, 1 as i64);
    return a[i] + s.a[i];
}
function borrowed(s: S, i: i32): i64 {
    var a: i64[] = s.a;
    a = a.with(i, 1 as i64);
    return a[i];
}
function main(): i32 {
    var s: S = S { a: [0 as i64], b: ["x"], n: 1 };
    s = viaVar(s, 0);
    s = viaPattern(s, 0);
    s = inLoop(s, 2);
    var u: i64 = borrowed(s, 0);
    var r: i64 = rereads(S { a: [0 as i64], b: ["y"], n: 1 }, 0);
    return (r + u + viaPatternKeeps(1)) as i32;
}`

func TestOwnFieldLocalMoveIsClaimed(t *testing.T) {
	ip := lowerForTest(t, ownFieldLocalSrc)
	for _, fn := range []string{"viaVar", "viaPattern"} {
		if n := moveTests(fnNamed(t, ip, fn)); n == 0 {
			t.Errorf("%s reads its field out without the is_unique-gated move:\n%s", fn, ip)
		}
	}
	// rereads reads s.a again, inLoop re-runs the read every turn,
	// viaPatternKeeps reads its local s after destructuring it, and borrowed
	// does not own s at all.
	for _, fn := range []string{"rereads", "inLoop", "viaPatternKeeps", "borrowed"} {
		if n := moveTests(fnNamed(t, ip, fn)); n != 0 {
			t.Errorf("%s moves a field something later still reads (%d uniqueness tests):\n%s", fn, n, ip)
		}
	}
}

// moveTests counts the is_unique tests a field move emits: ones whose
// unique arm empties the slot. The test `.with` makes has an empty unique
// arm, so it is not counted.
func moveTests(fn *ir.Func) int {
	n := 0
	for i, op := range fn.Ops {
		if op.Kind != ir.OpRcIsUnique || i+2 >= len(fn.Ops) {
			continue
		}
		if fn.Ops[i+1].Kind == ir.OpIf && fn.Ops[i+2].Kind != ir.OpElse {
			n++
		}
	}
	return n
}
