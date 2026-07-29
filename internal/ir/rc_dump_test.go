package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// Pins the rcPlan dump format — the contract of the #4482 differential
// harness. The self-host dump driver must emit exactly this rendering, so
// any change here is a cross-compiler format change and must be mirrored.
// The program exercises the tables: a consumed-threaded param (thread), a
// moved local + move site (mover), and a precise drop (dropper).
func TestRcPlanDumpFormat(t *testing.T) {
	dumps := map[string]string{}
	ir.RcPlanHook = func(fn, dump string) { dumps[fn] = dump }
	defer func() { ir.RcPlanHook = nil }()

	lowerForTest(t, `struct Ctx { name: string, n: i32 }
struct Leaf { v: i32[] }
struct Node { kid: Ex }
type Ex = Leaf | Node;
function thread(c: Ctx): i32 {
	c = Ctx { name: "x", n: c.n + 1 };
	return c.n;
}
function wrapper(e: Ex): Ex {
	e = Node { kid: e };
	return e;
}
function mover(): i32 {
	var a: i32[] = [1, 2, 3];
	var b: i32[] = a;
	return b[0];
}
function dropper(): i32 {
	var big: i32[] = [1, 2, 3, 4];
	var s: i32 = big[0];
	return s + 1;
}
function main(): i32 { return thread(Ctx { name: "a", n: 1 }) + mover() + dropper() + wrapped(); }
function wrapped(): i32 {
	match (wrapper(Leaf { v: [1] })) {
		Leaf(l) => { return l.v[0]; },
		Node(n) => { return 2; },
	}
}`)

	check := func(fn string, wantLines ...string) {
		t.Helper()
		got := dumps[fn]
		for _, w := range wantLines {
			if !strings.Contains(got, w) {
				t.Errorf("%s dump missing %q; got:\n%s", fn, w, got)
			}
		}
	}
	// thread: the string-bearing struct param is promoted consumed-threaded
	// (and thereby freeEligible).
	check("thread", "consumedParams: c", "freeEligible: c")
	// A reassigned UNION-typed param is promoted the same way, so its entry inc
	// balances the reassignment's overwrite dec. Without the promotion that dec
	// releases a reference the borrow model never handed the callee — the
	// parse_postfix `base = e_unary_at(op, base, …)` under-count (see
	// TestX86_64UnionThreadedParam). It escapes into the returned node, so it is
	// (correctly) not freeEligible: nothing is dec'd again at exit.
	check("wrapper", "consumedParams: e")
	// mover: `var b = a` is a's last use — a moves into b.
	check("mover", "movedLocals: a", "moveSites: ")
	// dropper: big's last use is `big[0]` at top-level statement 1 — the
	// precise drop lands right after it.
	check("dropper", "freeEligible: big", "preciseDrops: 1=big")

	if len(dumps) == 0 {
		t.Fatal("RcPlanHook never fired")
	}
}
