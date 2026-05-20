package ssa

import "testing"

// TestFuncRPOEntryFirst — entry is always first in RPO.
func TestFuncRPOEntryFirst(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	f.SetBr(thenB, merge)
	f.SetBr(elseB, merge)
	f.SetRet(merge, Value{})

	rpo := f.RPO()
	if len(rpo) != 4 {
		t.Fatalf("RPO len = %d, want 4", len(rpo))
	}
	if rpo[0] != entry {
		t.Errorf("RPO[0] = %v, want entry", rpo[0])
	}
}

// TestFuncRPOMergeAfterBranches — in a diamond, merge appears
// after both then and else (forward-dataflow guarantee).
func TestFuncRPOMergeAfterBranches(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	f.SetBr(thenB, merge)
	f.SetBr(elseB, merge)
	f.SetRet(merge, Value{})

	rpo := f.RPO()
	pos := map[*Block]int{}
	for i, b := range rpo {
		pos[b] = i
	}
	if pos[merge] < pos[thenB] || pos[merge] < pos[elseB] {
		t.Errorf("merge (idx %d) must come after both then (%d) and else (%d)",
			pos[merge], pos[thenB], pos[elseB])
	}
}

// TestFuncRPOSkipsUnreachable — island block not reached
// from entry is excluded.
func TestFuncRPOSkipsUnreachable(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	island := f.NewBlock()
	f.SetRet(entry, Value{})
	f.SetRet(island, Value{})

	rpo := f.RPO()
	for _, b := range rpo {
		if b == island {
			t.Errorf("unreachable island included in RPO: %v", rpo)
		}
	}
	if len(rpo) != 1 {
		t.Errorf("RPO len = %d, want 1 (only entry)", len(rpo))
	}
}

// TestFuncRPOAgreesWithDomTree — public Func.RPO matches the
// dom-tree's cached RPO walk for the same function.
func TestFuncRPOAgreesWithDomTree(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	done := f.NewBlock()
	f.SetBr(entry, header)
	f.SetBrIf(header, c, body, done)
	f.SetBr(body, header)
	f.SetRet(done, Value{})

	pub := f.RPO()
	dom := BuildDomTree(f).RPO()
	if len(pub) != len(dom) {
		t.Fatalf("len(Func.RPO()) = %d vs len(DomTree.RPO()) = %d", len(pub), len(dom))
	}
	for i := range pub {
		if pub[i] != dom[i] {
			t.Errorf("RPO[%d] mismatch: pub=%v dom=%v", i, pub[i], dom[i])
		}
	}
}

// TestFuncRPONilFunc — defensive.
func TestFuncRPONilFunc(t *testing.T) {
	var f *Func
	if got := f.RPO(); got != nil {
		t.Errorf("(*Func)(nil).RPO() = %v, want nil", got)
	}
}

// TestFuncRPOEmptyFunc — Func with no blocks returns nil.
func TestFuncRPOEmptyFunc(t *testing.T) {
	f := NewFunc("f")
	if got := f.RPO(); got != nil {
		t.Errorf("empty Func.RPO() = %v, want nil", got)
	}
}
