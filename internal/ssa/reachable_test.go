package ssa

import "testing"

// TestReachableAllConnected — every block in a diamond CFG is
// reachable from entry.
func TestReachableAllConnected(t *testing.T) {
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

	r := Reachable(f)
	for _, b := range []*Block{entry, thenB, elseB, merge} {
		if !r[b] {
			t.Errorf("block %d not reachable; want reachable", b.ID)
		}
	}
	if len(r) != 4 {
		t.Errorf("Reachable size = %d, want 4", len(r))
	}
}

// TestReachableSkipsIsland — a block with no inbound edges
// from anywhere reachable is excluded.
func TestReachableSkipsIsland(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	island := f.NewBlock()
	f.SetRet(entry, Value{})
	f.SetRet(island, Value{})

	r := Reachable(f)
	if !r[entry] {
		t.Error("entry not in Reachable")
	}
	if r[island] {
		t.Error("island reachable; want false")
	}
}

// TestReachableLoop — back-edge doesn't confuse the DFS.
func TestReachableLoop(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	done := f.NewBlock()
	f.SetBr(entry, header)
	f.SetBrIf(header, c, body, done)
	f.SetBr(body, header) // back-edge
	f.SetRet(done, Value{})

	r := Reachable(f)
	if len(r) != 4 {
		t.Errorf("Reachable size = %d, want 4", len(r))
	}
}

// TestIsReachable — direct query for a specific block.
func TestIsReachable(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	next := f.NewBlock()
	island := f.NewBlock()
	f.SetBr(entry, next)
	f.SetRet(next, Value{})
	f.SetRet(island, Value{})

	if !IsReachable(f, entry) {
		t.Error("entry not reachable")
	}
	if !IsReachable(f, next) {
		t.Error("next not reachable")
	}
	if IsReachable(f, island) {
		t.Error("island reachable; want false")
	}
}

// TestReachableNilFunc — defensive.
func TestReachableNilFunc(t *testing.T) {
	r := Reachable(nil)
	if r == nil {
		t.Error("Reachable(nil) returned nil; want empty map")
	}
	if len(r) != 0 {
		t.Errorf("len = %d, want 0", len(r))
	}
	if IsReachable(nil, nil) {
		t.Error("IsReachable(nil, nil) = true, want false")
	}
}

// TestReachableMatchesPrune — Reachable agrees with what
// PruneUnreachable removes (round-trip property: removed
// blocks are exactly those NOT in Reachable).
func TestReachableMatchesPrune(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	live := f.NewBlock()
	dead := f.NewBlock()
	f.SetBr(entry, live)
	f.SetRet(live, Value{})
	f.SetRet(dead, Value{}) // no inbound

	r := Reachable(f)
	if r[dead] {
		t.Error("dead block reachable")
	}

	c := f.Clone()
	removed := PruneUnreachable(c)
	if removed != 1 {
		t.Errorf("PruneUnreachable removed = %d, want 1", removed)
	}
	if len(c.Blocks) != len(r) {
		t.Errorf("after prune, Blocks len = %d; Reachable size = %d", len(c.Blocks), len(r))
	}
}
