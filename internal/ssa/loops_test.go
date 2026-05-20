package ssa

import "testing"

// TestLoopsSimpleWhile — `while (c) { body }` CFG:
//
//	entry → header
//	header → body, done   (brif)
//	body → header         (back-edge)
//
// One loop: Header=header, Body={header, body}, one back-edge.
func TestLoopsSimpleWhile(t *testing.T) {
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

	loops := Loops(f)
	if len(loops) != 1 {
		t.Fatalf("Loops = %d, want 1", len(loops))
	}
	lp := loops[0]
	if lp.Header != header {
		t.Errorf("Header = %v, want header", lp.Header)
	}
	if !lp.Body[header] || !lp.Body[body] {
		t.Errorf("Body missing header or body; got %v", lp.Body)
	}
	if lp.Body[entry] || lp.Body[done] {
		t.Errorf("Body includes entry or done: %v", lp.Body)
	}
	if len(lp.BackEdges) != 1 {
		t.Errorf("BackEdges = %d, want 1", len(lp.BackEdges))
	}
	if lp.BackEdges[0].Tail != body || lp.BackEdges[0].Header != header {
		t.Errorf("BackEdge = %+v, want body→header", lp.BackEdges[0])
	}
}

// TestLoopsNoLoop — a straight-line CFG has no loops.
func TestLoopsNoLoop(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	mid := f.NewBlock()
	f.SetBr(entry, mid)
	f.SetRet(mid, Value{})

	loops := Loops(f)
	if len(loops) != 0 {
		t.Errorf("Loops = %d, want 0", len(loops))
	}
}

// TestLoopsNested — outer loop wraps an inner one. Two loops
// expected; the inner one's header is dominated by the outer.
func TestLoopsNested(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	outer := f.NewBlock()
	inner := f.NewBlock()
	innerBody := f.NewBlock()
	outerBody := f.NewBlock()
	done := f.NewBlock()
	f.SetBr(entry, outer)
	f.SetBrIf(outer, c, inner, done)
	f.SetBrIf(inner, c, innerBody, outerBody)
	f.SetBr(innerBody, inner) // inner back-edge
	f.SetBr(outerBody, outer) // outer back-edge
	f.SetRet(done, Value{})

	loops := Loops(f)
	if len(loops) != 2 {
		t.Fatalf("Loops = %d, want 2", len(loops))
	}
	// One loop has Header=outer (includes inner, innerBody,
	// outerBody, outer), other has Header=inner (includes
	// inner, innerBody).
	var outerLoop, innerLoop *Loop
	for _, lp := range loops {
		switch lp.Header {
		case outer:
			outerLoop = lp
		case inner:
			innerLoop = lp
		}
	}
	if outerLoop == nil || innerLoop == nil {
		t.Fatalf("missing outer or inner loop; loops=%+v", loops)
	}
	if !outerLoop.Body[inner] || !outerLoop.Body[innerBody] {
		t.Errorf("outer loop missing nested blocks; body=%v", outerLoop.Body)
	}
	if innerLoop.Body[outerBody] {
		t.Errorf("inner loop wrongly includes outerBody; body=%v", innerLoop.Body)
	}
}

// TestLoopsMultipleBackEdges — two back-edges into the same
// header (e.g. continue inside an if-else) merge into one
// Loop with two BackEdges.
func TestLoopsMultipleBackEdges(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	header := f.NewBlock()
	branch := f.NewBlock()
	tailA := f.NewBlock()
	tailB := f.NewBlock()
	done := f.NewBlock()
	f.SetBr(entry, header)
	f.SetBrIf(header, c, branch, done)
	f.SetBrIf(branch, c, tailA, tailB)
	f.SetBr(tailA, header) // back-edge 1
	f.SetBr(tailB, header) // back-edge 2
	f.SetRet(done, Value{})

	loops := Loops(f)
	if len(loops) != 1 {
		t.Fatalf("Loops = %d, want 1 (merged)", len(loops))
	}
	if len(loops[0].BackEdges) != 2 {
		t.Errorf("BackEdges = %d, want 2", len(loops[0].BackEdges))
	}
	for _, b := range []*Block{header, branch, tailA, tailB} {
		if !loops[0].Body[b] {
			t.Errorf("Body missing %v", b)
		}
	}
}

// TestLoopsSelfLoop — a block whose terminator points at
// itself is a single-block loop.
func TestLoopsSelfLoop(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	loop := f.NewBlock()
	done := f.NewBlock()
	f.SetBr(entry, loop)
	f.SetBrIf(loop, c, loop, done) // self-loop on the true branch
	f.SetRet(done, Value{})

	loops := Loops(f)
	if len(loops) != 1 {
		t.Fatalf("Loops = %d, want 1", len(loops))
	}
	if loops[0].Header != loop {
		t.Errorf("Header = %v, want loop", loops[0].Header)
	}
	if len(loops[0].Body) != 1 || !loops[0].Body[loop] {
		t.Errorf("Body = %v, want {loop}", loops[0].Body)
	}
}

// TestIsLoopHeader — convenience predicate.
func TestIsLoopHeader(t *testing.T) {
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

	if !IsLoopHeader(f, header) {
		t.Errorf("IsLoopHeader(header) = false, want true")
	}
	if IsLoopHeader(f, entry) {
		t.Errorf("IsLoopHeader(entry) = true, want false")
	}
	if IsLoopHeader(f, body) {
		t.Errorf("IsLoopHeader(body) = true, want false")
	}
}

// TestLoopsNilFunc — defensive.
func TestLoopsNilFunc(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Loops(nil) panicked: %v", r)
		}
	}()
	Loops(nil)
}
