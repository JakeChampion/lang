// computeReturnOwnMoves claims a `return f(…, p, …)` that hands an `own` param
// on to another `own` parameter, so the argument drops its compensating retain
// and that ONE return's sweep skips p. The runtime consequences are pinned in
// internal/e2e/rc_own_remove_test.go; these cases pin the claim's boundary,
// where the observable is which rc ops are emitted rather than how many bytes
// move.
package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// rcIncCount counts the retains in fn — the op the claim removes.
func rcIncCount(fn *ir.Func) int {
	n := 0
	for _, op := range fn.Ops {
		if op.Str == "__fern_rc_inc" {
			n++
		}
	}
	return n
}

const ownMovePrelude = `struct P { data: i32[], n: i32 }
function inner(own p: P, k: i32): P { p = P { ...p, data: p.data.append(k) }; return p; }
function inner2(k: i32, own p: P): P { p = P { ...p, data: p.data.append(k) }; return p; }
`

const ownMoveDriver = `
function main(): i32 { var p: P = P { data: [], n: 0 }; p = outer(p, 1); return p.data.len(); }`

// The claim's whole point: whether the transfer happens to be p's textually
// LAST occurrence must stop mattering. These two functions differ only in
// which arm comes first — in `last` the transfer is the last occurrence, so
// move-on-call claimed it long before this change; in `notLast` the bare
// `return p` is, so the transfer used to pay a retain instead. Asserting they
// agree says exactly that, without depending on the absolute count (the bare
// `return p` carries a transfer inc of its own, which is not what this is
// about).
func TestReturnOwnMoveMakesTransferOrderIrrelevant(t *testing.T) {
	notLast := lowerForTest(t, ownMovePrelude+`function outer(own p: P, k: i32): P {
    if (k >= 0) { return inner(p, k); }
    return p;
}`+ownMoveDriver)
	last := lowerForTest(t, ownMovePrelude+`function outer(own p: P, k: i32): P {
    if (k < 0) { return p; }
    return inner(p, k);
}`+ownMoveDriver)
	gotNotLast := rcIncCount(fnNamed(t, notLast, "outer"))
	gotLast := rcIncCount(fnNamed(t, last, "outer"))
	if gotNotLast != gotLast {
		t.Errorf("__fern_rc_inc count = %d when the transfer is not p's last occurrence "+
			"vs %d when it is; the two shapes transfer p identically and must cost the same. "+
			"The excess is the retain compensating for a sweep the whole-function analysis "+
			"could not suppress", gotNotLast, gotLast)
	}
}

// Does NOT fire: p occurs twice in the return — a borrow and then the transfer
// — so one sweep exclusion cannot describe the site. (Only this spelling is
// expressible: E050 rejects a borrow AFTER the consume, and two consumes even
// more so.) A `return p` later in the function keeps the whole-function
// analysis off the site too, so what is left is the compensating retain.
//
// This one would in fact be safe to claim — the borrow transfers nothing. The
// rule stays syntactic so the claim is checkable at the site, rather than
// leaning on the checker having ruled out the shapes that would not be.
func TestReturnOwnMoveDefersOnSecondOccurrence(t *testing.T) {
	ip := lowerForTest(t, ownMovePrelude+`function outer(own p: P, k: i32): P {
    if (k > 0) { return inner2(p.n + k, p); }
    return p;
}`+ownMoveDriver)
	if got := rcIncCount(fnNamed(t, ip, "outer")); got < 2 {
		t.Errorf("outer emits %d __fern_rc_inc — the second occurrence of p should have left "+
			"the site unclaimed, so its retain is still there alongside the bare return's "+
			"transfer inc", got)
	}
}
