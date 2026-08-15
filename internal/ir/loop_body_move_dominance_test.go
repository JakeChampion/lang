package ir

import (
	"fmt"
	"testing"
)

// The loop-body move's dominance guard is the interval between a var's
// DECLARATION and the construction that consumes it: an early exit inside that
// interval leaves a path that builds the box and never hands it to the
// container, and `moved` is function-wide, so nothing releases it. An early exit
// outside the interval cannot do that — before the declaration no box exists
// yet, and after the construction the container owns it (#6533).
//
// The guard used to be per-BODY: any `return` / `break` / `continue` anywhere in
// the loop disqualified every construction in it, so a guard clause on a
// provably dead branch cost 1280 B/round on both natives and 960 B/round on
// wasm. Every parser and tokeniser is built out of that shape.
//
// Each case is judged against `boundPushSrc` (the same loop with no early exit
// at all), which already moves: an eligible placement must lower to the SAME rc
// traffic, and an ineligible one must keep its retain.

// pushLoopSrc builds the accumulate-into-an-array loop with `pre` before the
// element's declaration, `mid` between the declaration and the push, and `post`
// after it.
func pushLoopSrc(pre, mid, post string) string {
	return fmt.Sprintf(`struct Val { kind: i32, kids: i32[] }
function build(n: i32): i32 {
    var vals: Val[] = [];
    var total: i32 = 0;
    for i in 0..n {
%s        var v = Val { kind: i, kids: [] };
%s        vals = vals.append(v);
%s        total = total + vals.len();
    }
    return total;
}`, pre, mid, post)
}

func TestLoopBodyMoveDominance(t *testing.T) {
	const guard = "        if (i == 9999) { return 12345; }\n"
	const guardBreak = "        if (i == 9999) { break; }\n"
	const guardCont = "        if (i == 9999) { continue; }\n"

	base := countRcIncs(lowerSource(t, boundPushSrc), "build")
	if base < 0 {
		t.Fatalf("baseline not lowered")
	}

	for _, tc := range []struct {
		name             string
		pre, mid, post   string
		wantMoveAdmitted bool
	}{
		{"guard clause before the declaration", guard, "", "", true},
		{"break before the declaration", guardBreak, "", "", true},
		{"continue before the declaration", guardCont, "", "", true},
		{"return after the push", "", "", guard, true},
		{"break after the push", "", "", guardBreak, true},
		{"continue after the push", "", "", guardCont, true},
		{"return between the declaration and the push", "", guard, "", false},
		{"break between the declaration and the push", "", guardBreak, "", false},
		{"continue between the declaration and the push", "", guardCont, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := lowerSource(t, pushLoopSrc(tc.pre, tc.mid, tc.post))
			if findFunc(p, "build") == nil {
				t.Fatal("build not lowered")
			}
			got := countRcIncs(p, "build")
			if tc.wantMoveAdmitted {
				if got != base {
					t.Errorf("emits %d rc.inc, the same loop with no early exit emits %d — "+
						"the element kept a retain the exit cannot have made conditional, "+
						"and the escape taint suppresses the matching release", got, base)
				}
				return
			}
			if got <= base {
				t.Errorf("emits %d rc.inc, the same loop with no early exit emits %d — "+
					"an exit between the declaration and the push leaves a path that "+
					"builds the element and never stores it, so the element must keep "+
					"its retain", got, base)
			}
		})
	}
}

// `?` returns the failure variant from the enclosing function, so it exits the
// loop body exactly as `return` does. It is not a statement, so a guard that
// looks only for Return / Break / Continue never saw it.
func TestLoopBodyMoveTryOpBetweenDeclAndPushNotMoved(t *testing.T) {
	src := `struct Val { kind: i32, kids: i32[] }
function step(i: i32): Option[i32] {
    if (i < 0) { return None; }
    return Some(i);
}
function build(n: i32): Option[i32] {
    var vals: Val[] = [];
    var total: i32 = 0;
    for i in 0..n {
        var v = Val { kind: i, kids: [] };
        var w: i32 = step(i)?;
        vals = vals.append(v);
        total = total + vals.len() + w;
    }
    return Some(total);
}`
	p := lowerSource(t, src)
	if findFunc(p, "build") == nil {
		t.Fatal("build not lowered")
	}
	if countRcIncs(p, "build") == 0 {
		t.Error("no rc.inc: a `?` between the element's declaration and the push " +
			"can leave the loop with the element built and unstored, so the element " +
			"must keep its retain")
	}
}
