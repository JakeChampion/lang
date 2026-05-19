package ssa

import "testing"

// TestDomLinearChain — entry → a → b → c. Each block's idom
// is its sole predecessor; entry is its own idom.
func TestDomLinearChain(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.NewBlock()
	b := f.NewBlock()
	c := f.NewBlock()
	f.SetBr(entry, a)
	f.SetBr(a, b)
	f.SetBr(b, c)
	f.SetRet(c, Value{})

	d := BuildDomTree(f)

	if d.Idom[entry] != entry {
		t.Errorf("idom[entry] = %v, want entry self-loop", d.Idom[entry])
	}
	if d.Idom[a] != entry {
		t.Errorf("idom[a] = %v, want entry", d.Idom[a])
	}
	if d.Idom[b] != a {
		t.Errorf("idom[b] = %v, want a", d.Idom[b])
	}
	if d.Idom[c] != b {
		t.Errorf("idom[c] = %v, want b", d.Idom[c])
	}
}

// TestDomDiamond — entry branches to then/else, both join at
// merge. The merge's idom is the entry (the LCA of its two
// preds in the tree), not either branch.
func TestDomDiamond(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, cond, thenB, elseB)
	f.SetBr(thenB, merge)
	f.SetBr(elseB, merge)
	f.SetRet(merge, Value{})

	d := BuildDomTree(f)

	if d.Idom[entry] != entry {
		t.Errorf("idom[entry] = %v, want self", d.Idom[entry])
	}
	if d.Idom[thenB] != entry {
		t.Errorf("idom[thenB] = %v, want entry", d.Idom[thenB])
	}
	if d.Idom[elseB] != entry {
		t.Errorf("idom[elseB] = %v, want entry", d.Idom[elseB])
	}
	if d.Idom[merge] != entry {
		t.Errorf("idom[merge] = %v, want entry (LCA of thenB+elseB)", d.Idom[merge])
	}
}

// TestDomLoop — entry → header → body → header (back-edge);
// header also exits to done. Header's idom is entry. Body's
// idom is header. Done's idom is header (only path to done
// goes through header).
func TestDomLoop(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	done := f.NewBlock()
	f.SetBr(entry, header)
	f.SetBrIf(header, cond, body, done)
	f.SetBr(body, header) // back-edge
	f.SetRet(done, Value{})

	d := BuildDomTree(f)

	if d.Idom[entry] != entry {
		t.Errorf("idom[entry] = %v, want self", d.Idom[entry])
	}
	if d.Idom[header] != entry {
		t.Errorf("idom[header] = %v, want entry", d.Idom[header])
	}
	if d.Idom[body] != header {
		t.Errorf("idom[body] = %v, want header", d.Idom[body])
	}
	if d.Idom[done] != header {
		t.Errorf("idom[done] = %v, want header", d.Idom[done])
	}
}

// TestDominates — reflexivity, direct dom, transitive dom,
// non-dom across siblings.
func TestDominates(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, cond, thenB, elseB)
	f.SetBr(thenB, merge)
	f.SetBr(elseB, merge)
	f.SetRet(merge, Value{})

	d := BuildDomTree(f)

	if !d.Dominates(entry, entry) {
		t.Error("entry should dominate itself")
	}
	if !d.Dominates(entry, thenB) {
		t.Error("entry should dominate thenB")
	}
	if !d.Dominates(entry, merge) {
		t.Error("entry should dominate merge")
	}
	if d.Dominates(thenB, merge) {
		t.Error("thenB should NOT dominate merge (else branch bypasses it)")
	}
	if d.Dominates(thenB, elseB) {
		t.Error("thenB should NOT dominate elseB (siblings)")
	}
}

// TestDomUnreachableSkipped — a block with no path from entry
// isn't in the Idom map. Verify() proper rejects this, but the
// dom builder shouldn't panic on it either.
func TestDomUnreachableSkipped(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	dead := f.NewBlock()
	f.SetRet(entry, Value{})
	f.SetRet(dead, Value{})

	d := BuildDomTree(f)

	if _, ok := d.Idom[dead]; ok {
		t.Errorf("dead block should be absent from Idom map, got %v", d.Idom[dead])
	}
	if d.Idom[entry] != entry {
		t.Errorf("entry idom should be self even with unreachable blocks present")
	}
}

// TestDomNilFunc — BuildDomTree(nil) returns an empty tree,
// not a panic. Defensive for analysis-pass plumbing.
func TestDomNilFunc(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("BuildDomTree(nil) panicked: %v", r)
		}
	}()
	d := BuildDomTree(nil)
	if len(d.Idom) != 0 {
		t.Errorf("nil func: Idom = %v, want empty", d.Idom)
	}
}

// TestRPOOrdering — RPO has entry first; every block appears
// after at least one of its forward-edge predecessors (modulo
// back-edges, which are the explicit exception).
func TestRPOOrdering(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	entry := f.NewBlock()
	a := f.NewBlock()
	b := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, cond, a, b)
	f.SetBr(a, merge)
	f.SetBr(b, merge)
	f.SetRet(merge, Value{})

	d := BuildDomTree(f)
	rpo := d.RPO()
	if len(rpo) != 4 {
		t.Fatalf("RPO len = %d, want 4", len(rpo))
	}
	if rpo[0] != entry {
		t.Errorf("RPO[0] = %v, want entry", rpo[0])
	}
	// Merge has to come after both a and b.
	pos := map[*Block]int{}
	for i, b := range rpo {
		pos[b] = i
	}
	if pos[merge] < pos[a] || pos[merge] < pos[b] {
		t.Errorf("RPO order = %v; merge must follow both a (%d) and b (%d)",
			rpo, pos[a], pos[b])
	}
}
