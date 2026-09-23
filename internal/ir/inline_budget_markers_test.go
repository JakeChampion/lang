package ir

import (
	"strings"
	"testing"
)

// The tiny-leaf BUDGET is the half of #10019 that the size-policy sweep left
// counting markers, and it is only reachable over the unit ceiling: past
// inlineMaxUnitOps, Inline drops to tiny-leaf mode, where `allows` charges each
// splice against a budget and `spend` decrements it. Both measured
// len(cand.body), so under `-g` a body was charged for its markers — it could
// miss the budget it fits as code, and the budget drained faster than the code
// it bought. TestInlineSizeIgnoresLineMarkers covers CANDIDACY over the same
// ceiling; this covers what happens per site once a callee is admitted.
//
// Asked directly rather than through Inline: the budget is a running total, so
// a whole-program check reports only that the final op streams differ, not
// which decision diverged. Synthetic bodies are what let the budget be set
// exactly at the body's size, which is the one value that separates the two
// countings.
func TestTinyLeafBudgetIgnoresLineMarkers(t *testing.T) {
	const bodyOps = 12
	body := make([]Op, 0, bodyOps)
	for i := 0; i < bodyOps-1; i++ {
		body = append(body, Op{Kind: OpConstI32, I32: int32(i)})
	}
	body = append(body, Op{Kind: OpReturn})

	marked := make([]Op, 0, 2*len(body))
	for _, op := range body {
		marked = append(marked, Op{Kind: OpLine, Str: "t.fern"}, op)
	}
	if len(marked) <= len(body) {
		t.Fatalf("the marked body is not longer (%d vs %d); this test would be vacuous", len(marked), len(body))
	}

	// The budget is exactly the body's code size: it fits, and fits only
	// while the markers are not charged to it.
	plain := inlineCandidate{fn: &Func{Name: "leaf", Ops: body}, body: body}
	debug := inlineCandidate{fn: &Func{Name: "leaf", Ops: marked}, body: marked}

	mp := &inlineMode{tinyLeaf: true, budget: bodyOps}
	md := &inlineMode{tinyLeaf: true, budget: bodyOps}
	if a, b := mp.allows(plain, 0, false), md.allows(debug, 0, false); a != b {
		t.Errorf("tiny-leaf budget admits %v without -g and %v with", a, b)
	}
}

// The other half of the budget is what each splice COSTS. Charge it
// len(cand.body) and a `-g` build buys fewer splices from the same budget, so
// the call sites late in a function are refused over markers that emit no
// code — #10019 again, reached through the running total rather than through a
// single decision.
//
// A running total is only observable whole-program: a spend the test computes
// itself would hold however inlineOps charges. So this builds one program both
// ways and compares what came out, with the budget deliberately binding in
// BOTH builds. Every premise that makes the case able to fail is asserted
// below — a fixture whose budget never binds, or whose leaf differs in
// CANDIDACY rather than cost, would pass against a len-charged spend.
func TestTinyLeafBudgetSpendIgnoresLineMarkers(t *testing.T) {
	const sites = 400
	var b strings.Builder
	b.WriteString(padStmts(4000))
	for i := 0; i < sites; i++ {
		b.WriteString("acc = acc + leaf(acc);\n")
	}
	// `leaf` is under the tiny cap counted BOTH ways, so it is admitted in
	// both builds and only the charge can differ.
	src := `function leaf(x: i32): i32 {
			var a: i32 = x + 1;
			a = a * 3;
			a = a + 2;
			return a;
		}
		function main(): i32 {
		` + b.String() + `
			return acc;
		}`

	plain, debug := lowerBothWays(t, src)

	unit := programOps(plain)
	if other := programOps(debug); unit != other {
		t.Fatalf("the two builds size the unit differently (%d vs %d), so they start from different budgets", unit, other)
	}
	if unit <= inlineMaxUnitOps {
		t.Fatalf("the unit is %d ops, at or under the %d ceiling — the general mode runs and the budget is never consulted", unit, inlineMaxUnitOps)
	}
	budget := unit / inlineTinyBudgetDivisor
	code, marked := sizeOps(findFunc(plain, "leaf").Ops), len(findFunc(debug, "leaf").Ops)
	if marked <= code {
		t.Fatalf("`leaf` is %d ops with markers and %d without; -g added nothing to charge", marked, code)
	}
	if marked > inlineTinyLeafOps {
		t.Fatalf("`leaf` is %d ops with its markers, over the %d tiny cap — then CANDIDACY is what differs, which is TestInlineSizeIgnoresLineMarkers' case and not this one", marked, inlineTinyLeafOps)
	}
	if budget/marked >= budget/code {
		t.Fatalf("a len-charged budget buys as many splices as a code-charged one (%d vs %d); as written this test cannot fail", budget/marked, budget/code)
	}
	if sites <= budget/code {
		t.Fatalf("%d call sites cannot exhaust a budget that buys %d splices — every site would splice either way and this test cannot fail", sites, budget/code)
	}

	Inline(plain)
	Inline(debug)

	left := countCallDirect(findFunc(plain, "main").Ops, "leaf")
	if left <= 0 || left >= sites {
		t.Fatalf("the budget did not bind as intended: %d of %d calls survived without -g", left, sites)
	}
	if got := countCallDirect(findFunc(debug, "main").Ops, "leaf"); got != left {
		t.Errorf("%d of %d calls survived without -g and %d with — the markers were charged against the budget", left, sites, got)
	}
	assertSameCode(t, plain, debug)
}
