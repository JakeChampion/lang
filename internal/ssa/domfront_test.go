package ssa

import (
	"sort"
	"testing"
)

// frontierIDs flattens DF into a stable sortable form for
// table-driven comparison.
func frontierIDs(df map[*Block][]*Block) map[int32][]int32 {
	out := map[int32][]int32{}
	for b, list := range df {
		ids := make([]int32, len(list))
		for i, x := range list {
			ids[i] = x.ID
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		out[b.ID] = ids
	}
	return out
}

// TestDFLinearChain — entry → a → b → c. No join points
// (every block has 1 pred), so every frontier is empty.
func TestDFLinearChain(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.NewBlock()
	b := f.NewBlock()
	c := f.NewBlock()
	f.SetBr(entry, a)
	f.SetBr(a, b)
	f.SetBr(b, c)
	f.SetRet(c, Value{})

	df := DominanceFrontier(f)
	if len(df) != 0 {
		t.Errorf("frontier should be empty for linear chain; got %v", frontierIDs(df))
	}
}

// TestDFDiamond — entry → {then, else} → merge.
//
// Both then and else have merge in their frontier (each
// branch's influence ends at the join). Entry has empty
// frontier (it dominates merge).
func TestDFDiamond(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	entry := f.NewBlock() // ID 1
	thenB := f.NewBlock() // ID 2
	elseB := f.NewBlock() // ID 3
	merge := f.NewBlock() // ID 4
	f.SetBrIf(entry, cond, thenB, elseB)
	f.SetBr(thenB, merge)
	f.SetBr(elseB, merge)
	f.SetRet(merge, Value{})

	df := DominanceFrontier(f)
	ids := frontierIDs(df)

	if got, want := ids[thenB.ID], []int32{merge.ID}; !equalIDs(got, want) {
		t.Errorf("DF[thenB] = %v, want %v", got, want)
	}
	if got, want := ids[elseB.ID], []int32{merge.ID}; !equalIDs(got, want) {
		t.Errorf("DF[elseB] = %v, want %v", got, want)
	}
	if got := ids[entry.ID]; len(got) != 0 {
		t.Errorf("DF[entry] = %v, want empty", got)
	}
	if got := ids[merge.ID]; len(got) != 0 {
		t.Errorf("DF[merge] = %v, want empty (no successor)", got)
	}
}

// TestDFLoop — entry → header → body → header (back-edge);
// header → done.
//
// Body's frontier is {header} — body dominates the back-edge
// pred (itself) and doesn't dominate header.
// Header's frontier is {header} too — header dominates body
// (a pred of header via the back-edge) but doesn't strictly
// dominate itself.
func TestDFLoop(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	entry := f.NewBlock()  // ID 1
	header := f.NewBlock() // ID 2
	body := f.NewBlock()   // ID 3
	done := f.NewBlock()   // ID 4
	f.SetBr(entry, header)
	f.SetBrIf(header, cond, body, done)
	f.SetBr(body, header) // back-edge
	f.SetRet(done, Value{})

	df := DominanceFrontier(f)
	ids := frontierIDs(df)

	if got, want := ids[body.ID], []int32{header.ID}; !equalIDs(got, want) {
		t.Errorf("DF[body] = %v, want %v", got, want)
	}
	if got, want := ids[header.ID], []int32{header.ID}; !equalIDs(got, want) {
		t.Errorf("DF[header] = %v, want %v", got, want)
	}
}

// TestDFIfWithEarlyReturn — entry → {then-ret, after};
// after → done.
//
//	   entry
//	  /     \
//	then    after
//	ret      |
//	        done
//
// After has empty frontier — only one predecessor, no join.
// Then has empty frontier — it has no successor.
// Entry has empty frontier — dominates everything reachable.
func TestDFIfWithEarlyReturn(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	after := f.NewBlock()
	done := f.NewBlock()
	f.SetBrIf(entry, cond, thenB, after)
	f.SetRet(thenB, Value{})
	f.SetBr(after, done)
	f.SetRet(done, Value{})

	df := DominanceFrontier(f)
	if len(df) != 0 {
		t.Errorf("frontier should be empty; got %v", frontierIDs(df))
	}
}

// TestDFNestedDiamond — outer diamond with inner diamond
// nested inside one branch.
//
//	    entry                              entry
//	   /     \                            /     \
//	 outerT  outerF                    outerT  outerF
//	 /   \    |                        innerT innerF (etc.)
//	iT   iF   |
//	 \   /    |
//	  iM      |
//	   \     /
//	    merge
//
// We expect DF[innerT] = DF[innerF] = {innerMerge};
// DF[innerMerge] = {merge}; DF[outerF] = {merge}.
func TestDFNestedDiamond(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	outerT := f.NewBlock()
	outerF := f.NewBlock()
	innerT := f.NewBlock()
	innerF := f.NewBlock()
	innerMerge := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, a, outerT, outerF)
	f.SetBrIf(outerT, b, innerT, innerF)
	f.SetBr(innerT, innerMerge)
	f.SetBr(innerF, innerMerge)
	f.SetBr(innerMerge, merge)
	f.SetBr(outerF, merge)
	f.SetRet(merge, Value{})

	df := DominanceFrontier(f)
	ids := frontierIDs(df)

	if got, want := ids[innerT.ID], []int32{innerMerge.ID}; !equalIDs(got, want) {
		t.Errorf("DF[innerT] = %v, want %v", got, want)
	}
	if got, want := ids[innerF.ID], []int32{innerMerge.ID}; !equalIDs(got, want) {
		t.Errorf("DF[innerF] = %v, want %v", got, want)
	}
	if got, want := ids[innerMerge.ID], []int32{merge.ID}; !equalIDs(got, want) {
		t.Errorf("DF[innerMerge] = %v, want %v", got, want)
	}
	if got, want := ids[outerT.ID], []int32{merge.ID}; !equalIDs(got, want) {
		t.Errorf("DF[outerT] = %v, want %v", got, want)
	}
	if got, want := ids[outerF.ID], []int32{merge.ID}; !equalIDs(got, want) {
		t.Errorf("DF[outerF] = %v, want %v", got, want)
	}
	if got := ids[entry.ID]; len(got) != 0 {
		t.Errorf("DF[entry] = %v, want empty", got)
	}
}

// TestDFFromReusesDomTree — calling DominanceFrontierFrom
// with an externally-built DomTree gives the same answer as
// DominanceFrontier.
func TestDFFromReusesDomTree(t *testing.T) {
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
	a := DominanceFrontier(f)
	b := DominanceFrontierFrom(f, d)
	if len(a) != len(b) {
		t.Fatalf("len(DF) = %d vs %d", len(a), len(b))
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || !equalBlocks(va, vb) {
			t.Errorf("DF[%d] mismatch: %v vs %v", k.ID, va, vb)
		}
	}
}

// TestDFNilFunc — defensive: nil inputs give an empty map,
// not a panic.
func TestDFNilFunc(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DominanceFrontier(nil) panicked: %v", r)
		}
	}()
	if got := DominanceFrontier(nil); len(got) != 0 {
		t.Errorf("DF(nil) = %v, want empty", got)
	}
	if got := DominanceFrontierFrom(nil, nil); len(got) != 0 {
		t.Errorf("DFFrom(nil, nil) = %v, want empty", got)
	}
}

func equalIDs(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalBlocks(a, b []*Block) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[*Block]bool{}
	for _, x := range a {
		seen[x] = true
	}
	for _, x := range b {
		if !seen[x] {
			return false
		}
	}
	return true
}
