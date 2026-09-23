package ir

import "testing"

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

	// And the splice must cost the same, or a later site is refused under -g
	// for what an earlier one was charged.
	mp.spend(sizeOps(plain.body))
	md.spend(sizeOps(debug.body))
	if mp.budget != md.budget {
		t.Errorf("after one splice the budget is %d without -g and %d with", mp.budget, md.budget)
	}
}
