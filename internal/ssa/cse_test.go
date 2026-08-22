package ssa

import (
	"math"
	"testing"
)

// TestCSEDedupsAddInSameBlock — two `add p, p` ops in the
// same block both produce identical expressions. CSE aliases
// the second's result to the first, leaving the first
// canonical and (after DCE) the second collapses.
func TestCSEDedupsAddInSameBlock(t *testing.T) {
	f := NewFunc("f")
	p := f.AddParam()
	entry := f.NewBlock()
	first := f.AddOp(entry, OpAdd, p, p)
	second := f.AddOp(entry, OpAdd, p, p)
	sum := f.AddOp(entry, OpAdd, first, second)
	f.SetRet(entry, sum)

	CSE(f)
	DCE(f)

	if len(entry.Ops) != 2 {
		t.Fatalf("Ops len = %d, want 2 (one add p,p + final add)", len(entry.Ops))
	}
	if entry.Ops[1].Args[0] != first || entry.Ops[1].Args[1] != first {
		t.Errorf("final-add args = %v, want both = %v after CSE", entry.Ops[1].Args, first)
	}
}

// TestCSEAcrossDominatedBlocks — defining `x = a+b` in entry,
// then re-computing the same in a dominated successor, is
// merged.
func TestCSEAcrossDominatedBlocks(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	next := f.NewBlock()
	x := f.AddOp(entry, OpAdd, a, b)
	f.SetBr(entry, next)
	y := f.AddOp(next, OpAdd, a, b) // redundant
	sum := f.AddOp(next, OpAdd, x, y)
	f.SetRet(next, sum)

	CSE(f)
	DCE(f)

	// Expect: entry still has `x = a+b`. next has only the final
	// `sum = x+x` (y was aliased to x, so the `add a,b` in next
	// went dead).
	if len(next.Ops) != 1 {
		t.Fatalf("next.Ops = %d, want 1 (final sum); got %v",
			len(next.Ops), next.Ops)
	}
	if next.Ops[0].Args[0] != x || next.Ops[0].Args[1] != x {
		t.Errorf("sum.Args = %v, want [%v %v]", next.Ops[0].Args, x, x)
	}
}

// TestCSESkipsSideEffectOps — Call / Load / Store look
// identical structurally but can't be deduped — CSE leaves
// them alone.
func TestCSESkipsSideEffectOps(t *testing.T) {
	f := NewFunc("f")
	addr := f.AddParam()
	entry := f.NewBlock()
	c1 := f.AddOp(entry, OpCall, addr)
	c2 := f.AddOp(entry, OpCall, addr)
	l1 := f.AddOp(entry, OpLoad, addr)
	l2 := f.AddOp(entry, OpLoad, addr)
	sum := f.AddOp(entry, OpAdd, c1, c2)
	more := f.AddOp(entry, OpAdd, l1, l2)
	final := f.AddOp(entry, OpAdd, sum, more)
	f.SetRet(entry, final)

	CSE(f)
	DCE(f)

	// All four side-effect ops survive.
	kinds := map[OpKind]int{}
	for _, op := range entry.Ops {
		kinds[op.Kind]++
	}
	if kinds[OpCall] != 2 {
		t.Errorf("OpCall count = %d, want 2 (no CSE)", kinds[OpCall])
	}
	if kinds[OpLoad] != 2 {
		t.Errorf("OpLoad count = %d, want 2 (no CSE)", kinds[OpLoad])
	}
}

// TestCSEDoesNotMergeFromUnreachedSibling — two siblings
// both compute `a+b`; CSE must NOT merge across them since
// neither dominates the other. Without dominance the rewrite
// would move a use into a block where the canonical Value
// isn't in scope.
func TestCSEDoesNotMergeFromUnreachedSibling(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	x := f.AddOp(thenB, OpAdd, a, b)
	f.SetRet(thenB, x)
	y := f.AddOp(elseB, OpAdd, a, b)
	f.SetRet(elseB, y)

	CSE(f)

	if thenB.Term.Value != x {
		t.Errorf("thenB.Term.Value = %v, want %v (no cross-sibling merge)", thenB.Term.Value, x)
	}
	if elseB.Term.Value != y {
		t.Errorf("elseB.Term.Value = %v, want %v (no cross-sibling merge)", elseB.Term.Value, y)
	}
}

// TestCSEHonorsConstantImm — two const_int ops with different
// Imm values are NOT equivalent.
func TestCSEHonorsConstantImm(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	one := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	two := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 2
	sum := f.AddOp(entry, OpAdd, one, two)
	f.SetRet(entry, sum)

	CSE(f)

	if entry.Ops[2].Args[0] != one || entry.Ops[2].Args[1] != two {
		t.Errorf("sum args = %v, want [one two] (no false merge)", entry.Ops[2].Args)
	}
}

// TestCSEDedupsIdenticalConsts — two const_int ops with the
// SAME Imm DO merge.
func TestCSEDedupsIdenticalConsts(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 7
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 7
	sum := f.AddOp(entry, OpAdd, a, b)
	f.SetRet(entry, sum)

	CSE(f)
	DCE(f)

	if len(entry.Ops) != 2 {
		t.Fatalf("Ops = %d, want 2 (one const + one add)", len(entry.Ops))
	}
	if entry.Ops[1].Args[0] != a || entry.Ops[1].Args[1] != a {
		t.Errorf("sum args = %v, want [a a]", entry.Ops[1].Args)
	}
}

// TestCSEPropagatesThroughChain — CSE result rewrites kick a
// later expression key into a hit. Single pass works because
// RPO visits defs before uses.
func TestCSEPropagatesThroughChain(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	// First chain: (a+b) * (a+b)
	x1 := f.AddOp(entry, OpAdd, a, b)
	y1 := f.AddOp(entry, OpAdd, a, b)
	m1 := f.AddOp(entry, OpMul, x1, y1)
	// Second chain mirrors the first.
	x2 := f.AddOp(entry, OpAdd, a, b)
	y2 := f.AddOp(entry, OpAdd, a, b)
	m2 := f.AddOp(entry, OpMul, x2, y2)
	sum := f.AddOp(entry, OpAdd, m1, m2)
	f.SetRet(entry, sum)

	CSE(f)
	DCE(f)

	// Expect: one (a+b), one (a+b)*(a+b), one final add of m1+m1.
	if len(entry.Ops) != 3 {
		t.Fatalf("Ops = %d, want 3; got kinds %v", len(entry.Ops), opKinds(entry.Ops))
	}
}

// TestCSESkipsPhi — phi ops aren't candidates (their meaning
// depends on Block + Preds order).
func TestCSESkipsPhi(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	one := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 1
	f.SetBr(thenB, merge)
	two := f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Imm = 2
	f.SetBr(elseB, merge)
	phi1 := f.AddPhi(merge, one, two)
	phi2 := f.AddPhi(merge, one, two)
	sum := f.AddOp(merge, OpAdd, phi1, phi2)
	f.SetRet(merge, sum)

	CSE(f)

	// Both phis still distinct (their Args match but CSE
	// shouldn't dedup phis).
	phiCount := 0
	for _, op := range merge.Ops {
		if op.Kind == OpPhi {
			phiCount++
		}
	}
	if phiCount != 2 {
		t.Errorf("phi count = %d, want 2 (CSE must not merge phis)", phiCount)
	}
}

// TestCSENilFunc — defensive: nil input no-op.
func TestCSENilFunc(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CSE(nil) panicked: %v", r)
		}
	}()
	CSE(nil)
}

func opKinds(ops []*Op) []OpKind {
	out := make([]OpKind, len(ops))
	for i, o := range ops {
		out[i] = o.Kind
	}
	return out
}

// TestCSEHonorsNaNPayload — two NaN constants with different mantissas are
// different values, so neither may be merged into the other. Every NaN renders
// the same in decimal, so a key built from the formatted float collapsed them
// and a signalling payload came back carrying the quiet bit.
func TestCSEHonorsNaNPayload(t *testing.T) {
	const quiet, signalling = 0x7ff8000000000000, 0x7ff0000000000001

	f := NewFunc("f")
	entry := f.NewBlock()
	q := f.AddOp(entry, OpConstFloat)
	entry.Ops[0].F64 = math.Float64frombits(quiet)
	s := f.AddOp(entry, OpConstFloat)
	entry.Ops[1].F64 = math.Float64frombits(signalling)
	sum := f.AddOp(entry, OpFAdd, q, s)
	f.SetRet(entry, sum)

	CSE(f)

	if entry.Ops[2].Args[0] != q || entry.Ops[2].Args[1] != s {
		t.Errorf("sum args = %v, want [q s]: the two NaN payloads were merged", entry.Ops[2].Args)
	}
}

// TestCSEDedupsIdenticalNaNs — the same bit pattern twice DOES merge, so the
// payload key did not simply disable NaN dedup.
func TestCSEDedupsIdenticalNaNs(t *testing.T) {
	const signalling = 0x7ff0000000000001

	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstFloat)
	entry.Ops[0].F64 = math.Float64frombits(signalling)
	b := f.AddOp(entry, OpConstFloat)
	entry.Ops[1].F64 = math.Float64frombits(signalling)
	sum := f.AddOp(entry, OpFAdd, a, b)
	f.SetRet(entry, sum)

	CSE(f)

	if entry.Ops[2].Args[0] != a || entry.Ops[2].Args[1] != a {
		t.Errorf("sum args = %v, want [a a]", entry.Ops[2].Args)
	}
}
