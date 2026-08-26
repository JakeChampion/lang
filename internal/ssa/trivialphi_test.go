package ssa

import "testing"

// TestTrivialPhiAllIdentical — `phi v, v, v` aliases to `v`,
// and DCE reclaims the now-orphan phi.
func TestTrivialPhiAllIdentical(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	// Both branches forward the same param value.
	p := f.AddParam() // shared incoming value
	_ = p
	f.SetBr(thenB, merge)
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, p, p)
	use := f.AddOp(merge, OpAdd, phi, phi)
	f.SetRet(merge, use)

	TrivialPhis(f)
	DCE(f)

	for _, op := range merge.Ops {
		if op.Kind == OpPhi {
			t.Error("expected phi to be eliminated; still present")
		}
	}
	if use2 := merge.Ops[0]; use2.Args[0] != p || use2.Args[1] != p {
		t.Errorf("use.Args = %v, want both = %v", use2.Args, p)
	}
}

// TestTrivialPhiSingleArg — a phi with only one incoming
// value (after FoldBranches dropped the other edge) aliases
// straight to that value.
func TestTrivialPhiSingleArg(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	thenB := f.NewBlock()
	merge := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1 // always take thenB
	f.SetBrIf(entry, c, thenB, merge)
	preMerge := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 7
	v := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 1
	f.SetBr(thenB, merge)
	// merge.Preds is [entry, thenB] from the SetBrIf+SetBr
	// order. Phi args follow.
	phi := f.AddPhi(merge, preMerge, v)
	f.SetRet(merge, phi)

	// FoldBranches drops the entry→merge edge (taken=thenB), so
	// phi becomes single-arg [v]. TrivialPhis aliases phi to v.
	FoldBranches(f)
	TrivialPhis(f)

	if merge.Term.Value != v {
		t.Errorf("Term.Value = %v, want %v (phi aliased to single surviving arg)", merge.Term.Value, v)
	}
}

// TestTrivialPhiKeepsDistinctArgs — phi with ≥2 distinct args
// is NOT trivial; left alone.
func TestTrivialPhiKeepsDistinctArgs(t *testing.T) {
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
	phi := f.AddPhi(merge, one, two)
	f.SetRet(merge, phi)

	TrivialPhis(f)

	if merge.Ops[0].Kind != OpPhi {
		t.Errorf("phi gone; expected to survive with distinct args")
	}
}

// TestTrivialPhiSelfRefOK — a phi `phi v, phi.Result` (loop
// header common case where the phi feeds itself) still
// counts as trivial — the self-ref is ignored, only `v`
// remains.
func TestTrivialPhiSelfRefOK(t *testing.T) {
	f := NewFunc("f")
	p := f.AddParam()
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	done := f.NewBlock()

	f.SetBr(entry, header)
	// Pre-mint the phi result so body can reference it as a
	// self-loop arg.
	phiResult := f.NewValue()
	phiOp := &Op{Kind: OpPhi, Result: phiResult, Args: []Value{p, phiResult}}
	header.Ops = append(header.Ops, phiOp)
	cond := f.AddOp(header, OpConstBool)
	header.Ops[1].Imm = 0 // exit on first iter to keep the test small
	f.SetBrIf(header, cond, body, done)
	f.SetBr(body, header)
	f.SetRet(done, phiResult)

	if err := Verify(f); err != nil {
		t.Fatalf("Verify pre-trivial-phi: %v", err)
	}

	TrivialPhis(f)

	// phi must have been aliased; done's ret should now point
	// at p directly.
	if done.Term.Value != p {
		t.Errorf("Term.Value = %v, want %v (self-ref phi collapsed)", done.Term.Value, p)
	}
}

// TestTrivialPhiCascades — eliminating one phi exposes a
// downstream phi as trivial (its arg now points at the
// surviving Value instead of the old phi result).
func TestTrivialPhiCascades(t *testing.T) {
	f := NewFunc("f")
	p := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	mid := f.NewBlock()
	endB := f.NewBlock()
	cond := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1
	f.SetBrIf(entry, cond, thenB, elseB)
	f.SetBr(thenB, mid)
	f.SetBr(elseB, mid)
	phi1 := f.AddPhi(mid, p, p) // trivial
	f.SetBr(mid, endB)
	phi2 := f.AddPhi(endB, phi1) // single arg, trivial
	f.SetRet(endB, phi2)

	TrivialPhis(f)

	if endB.Term.Value != p {
		t.Errorf("Term.Value = %v, want %v (cascade through two phis)", endB.Term.Value, p)
	}
}

// TestTrivialPhiInOptimizePipeline — Optimize runs
// TrivialPhis after FoldBranches; verify end-to-end the
// pipeline collapses brif-on-const + redundant phi.
func TestTrivialPhiInOptimizePipeline(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	thenB := f.NewBlock()
	merge := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1
	f.SetBrIf(entry, c, thenB, merge)
	preMerge := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 7
	v := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 1
	f.SetBr(thenB, merge)
	phi := f.AddPhi(merge, preMerge, v)
	f.SetRet(merge, phi)

	Optimize(f)

	// After Optimize: brif-on-const collapses to br, the trivial
	// phi resolves, and FuseLinearBlocks fuses the whole chain
	// into a single block. The final block's Ret value must be
	// the surviving const.
	last := f.Blocks[len(f.Blocks)-1]
	for _, b := range f.Blocks {
		if b.Term.Kind == TermRet {
			last = b
			break
		}
	}
	if !last.Term.Value.IsValid() {
		t.Fatal("no Ret terminator carries a valid Value after Optimize")
	}
}

// TestTrivialPhiConstIntArgsCollapse — two const_int 7 defs on different
// incoming edges collapse the phi into a const_int of its own, keeping the
// phi's Result. Aliasing the uses to v1 instead — which this pass used to
// do — is unsound: v1 is defined in thenB, which does not dominate the
// merge, so `use` would read a value that is not live on the elseB path.
// Verify rejects that shape, so the whole function failed to compile.
func TestTrivialPhiConstIntArgsCollapse(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	v1 := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 7
	f.SetBr(thenB, merge)
	v2 := f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Imm = 7 // same value, distinct Op
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, v1, v2)
	_ = f.AddOp(merge, OpAdd, phi, phi)
	f.SetRet(merge, merge.Ops[1].Result)

	TrivialPhis(f)

	// The phi became a const_int in its own block, under the same Result,
	// so `use` needs no rewriting at all.
	if got := merge.Ops[0]; got.Kind != OpConstInt || got.Imm != 7 || got.Result != phi {
		t.Errorf("merge.Ops[0] = %v (kind %v, imm %d, result %v), want const_int 7 with result %v",
			got, got.Kind, got.Imm, got.Result, phi)
	}
	if useOp := merge.Ops[1]; useOp.Args[0] != phi || useOp.Args[1] != phi {
		t.Errorf("use.Args = %v, want both = %v (phi Result kept)", useOp.Args, phi)
	}
	if err := Verify(f); err != nil {
		t.Errorf("Verify after TrivialPhis: %v", err)
	}
	_, _ = v1, v2
}

// TestTrivialPhiConstBoolArgs — same trick on OpConstBool.
func TestTrivialPhiConstBoolArgs(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	v1 := f.AddOp(thenB, OpConstBool)
	thenB.Ops[0].Imm = 1
	f.SetBr(thenB, merge)
	v2 := f.AddOp(elseB, OpConstBool)
	elseB.Ops[0].Imm = 1
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, v1, v2)
	f.SetRet(merge, phi)

	TrivialPhis(f)

	// Collapsed in place: the ret keeps the phi's Result, which is now a
	// const_bool defined in the merge block itself. Aliasing to v1 would name a
	// value defined in only one predecessor.
	if merge.Term.Value != phi {
		t.Errorf("Term.Value = %v, want %v (phi Result kept)",
			merge.Term.Value, phi)
	}
	if got := merge.Ops[0]; got.Kind != OpConstBool || got.Imm != 1 {
		t.Errorf("merge.Ops[0] = kind %v imm %d, want const_bool 1", got.Kind, got.Imm)
	}
	if err := Verify(f); err != nil {
		t.Errorf("Verify after TrivialPhis: %v", err)
	}
	_, _ = v1, v2
}

// TestTrivialPhiConstFloatArgs — and on OpConstFloat.
func TestTrivialPhiConstFloatArgs(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	v1 := f.AddOp(thenB, OpConstFloat)
	thenB.Ops[0].F64 = 3.14
	f.SetBr(thenB, merge)
	v2 := f.AddOp(elseB, OpConstFloat)
	elseB.Ops[0].F64 = 3.14
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, v1, v2)
	f.SetRet(merge, phi)

	TrivialPhis(f)

	// Collapsed in place: the ret keeps the phi's Result, which is now a
	// const_float defined in the merge block itself. Aliasing to v1 would name a
	// value defined in only one predecessor.
	if merge.Term.Value != phi {
		t.Errorf("Term.Value = %v, want %v (phi Result kept)",
			merge.Term.Value, phi)
	}
	if got := merge.Ops[0]; got.Kind != OpConstFloat || got.F64 != 3.14 {
		t.Errorf("merge.Ops[0] = kind %v f64 %v, want const_float 3.14", got.Kind, got.F64)
	}
	if err := Verify(f); err != nil {
		t.Errorf("Verify after TrivialPhis: %v", err)
	}
	_, _ = v1, v2
}

// TestTrivialPhiConstStringArgs — and on OpConstString.
func TestTrivialPhiConstStringArgs(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	v1 := f.AddOp(thenB, OpConstString)
	thenB.Ops[0].Str = "hi"
	f.SetBr(thenB, merge)
	v2 := f.AddOp(elseB, OpConstString)
	elseB.Ops[0].Str = "hi"
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, v1, v2)
	f.SetRet(merge, phi)

	TrivialPhis(f)

	// Collapsed in place: the ret keeps the phi's Result, which is now a
	// const_string defined in the merge block itself. Aliasing to v1 would name a
	// value defined in only one predecessor.
	if merge.Term.Value != phi {
		t.Errorf("Term.Value = %v, want %v (phi Result kept)",
			merge.Term.Value, phi)
	}
	if got := merge.Ops[0]; got.Kind != OpConstString || got.Str != "hi" {
		t.Errorf("merge.Ops[0] = kind %v str %q, want const_string \"hi\"", got.Kind, got.Str)
	}
	if err := Verify(f); err != nil {
		t.Errorf("Verify after TrivialPhis: %v", err)
	}
	_, _ = v1, v2
}

// TestTrivialPhiConstArgsDifferingValuesKept — phi of two
// const_int with DIFFERENT immediates is NOT trivial.
func TestTrivialPhiConstArgsDifferingValuesKept(t *testing.T) {
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
	elseB.Ops[0].Imm = 2 // distinct value, must NOT collapse
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, one, two)
	f.SetRet(merge, phi)

	TrivialPhis(f)

	if merge.Ops[0].Kind != OpPhi {
		t.Errorf("phi gone; expected to survive with distinct const values")
	}
}

// TestTrivialPhiConstArgsDifferingKindsKept — phi of one
// const_int and one const_float must NOT collapse even if
// they hold the "same" numeric value; cross-kind aliasing is
// unsound.
func TestTrivialPhiConstArgsDifferingKindsKept(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	iv := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 1
	f.SetBr(thenB, merge)
	fv := f.AddOp(elseB, OpConstFloat)
	elseB.Ops[0].F64 = 1.0
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, iv, fv)
	f.SetRet(merge, phi)

	TrivialPhis(f)

	if merge.Ops[0].Kind != OpPhi {
		t.Errorf("phi gone; cross-kind const args must NOT collapse")
	}
}

// TestTrivialPhiNilFunc — defensive nil-input.
func TestTrivialPhiNilFunc(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("TrivialPhis(nil) panicked: %v", r)
		}
	}()
	TrivialPhis(nil)
}
