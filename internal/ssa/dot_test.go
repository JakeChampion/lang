package ssa

import (
	"strings"
	"testing"
)

// TestDotSimpleFunction — golden form for the canonical
// `f(a, b) = a + b` shape.
func TestDotSimpleFunction(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	sum := f.AddOp(entry, OpAdd, a, b)
	f.SetRet(entry, sum)

	dot := f.ToDot()
	for _, want := range []string{
		"digraph f {",
		"rankdir=TB",
		"block_1",
		"v3 = add v1, v2",
		"ret v3",
	} {
		if !strings.Contains(dot, want) {
			t.Errorf("DOT missing %q in:\n%s", want, dot)
		}
	}
}

// TestDotBranching — diamond CFG renders both labelled edges.
func TestDotBranching(t *testing.T) {
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

	dot := f.ToDot()
	for _, want := range []string{
		"block_1 -> block_2 [label=\"true\"]",
		"block_1 -> block_3 [label=\"false\"]",
		"block_2 -> block_4",
		"block_3 -> block_4",
	} {
		if !strings.Contains(dot, want) {
			t.Errorf("DOT missing %q in:\n%s", want, dot)
		}
	}
}

// TestDotHighlightsEntry — entry block gets distinct styling.
func TestDotHighlightsEntry(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	f.SetRet(entry, Value{})

	dot := f.ToDot()
	want := "block_1 [style=filled, fillcolor=lightblue]"
	if !strings.Contains(dot, want) {
		t.Errorf("DOT missing entry highlight %q in:\n%s", want, dot)
	}
}

// TestDotNilFunc — defensive: returns a placeholder graph
// rather than panicking.
func TestDotNilFunc(t *testing.T) {
	var f *Func
	got := f.ToDot()
	if !strings.Contains(got, "digraph nil") {
		t.Errorf("nil ToDot = %q, want sentinel", got)
	}
}

// TestDotNameSanitised — function name with special chars
// gets sanitised for the DOT identifier.
func TestDotNameSanitised(t *testing.T) {
	f := NewFunc("foo.bar/baz")
	entry := f.NewBlock()
	f.SetRet(entry, Value{})

	got := f.ToDot()
	if !strings.Contains(got, "digraph foo_bar_baz {") {
		t.Errorf("DOT missing sanitised name in:\n%s", got)
	}
}

// TestDotEmptyName — falls back to `anon`.
func TestDotEmptyName(t *testing.T) {
	f := NewFunc("")
	entry := f.NewBlock()
	f.SetRet(entry, Value{})

	got := f.ToDot()
	if !strings.Contains(got, "digraph anon {") {
		t.Errorf("DOT missing anon fallback in:\n%s", got)
	}
}

// TestDotMarksUnreachable — a block disconnected from Entry
// gets the red/mistyrose styling. The entry block stays
// lightblue.
func TestDotMarksUnreachable(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()  // ID 1 — reachable
	orphan := f.NewBlock() // ID 2 — never linked
	f.SetRet(entry, Value{})
	f.SetRet(orphan, Value{})

	dot := f.ToDot()
	if !strings.Contains(dot, "block_2 [style=\"filled,dashed\", fillcolor=mistyrose, color=red]") {
		t.Errorf("DOT missing unreachable styling for orphan; got:\n%s", dot)
	}
	if strings.Contains(dot, "block_1 [style=\"filled,dashed\"") {
		t.Errorf("DOT incorrectly marked entry as unreachable; got:\n%s", dot)
	}
}

// TestDotAllReachableNoMistyrose — a healthy function with no
// orphans renders without any mistyrose styling.
func TestDotAllReachableNoMistyrose(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	body := f.NewBlock()
	f.SetBr(entry, body)
	f.SetRet(body, Value{})

	dot := f.ToDot()
	if strings.Contains(dot, "mistyrose") {
		t.Errorf("DOT mistakenly marked a healthy block as unreachable; got:\n%s", dot)
	}
}

// TestDotLoopHasBackEdge — loop with header+body emits a
// back-edge.
func TestDotLoopHasBackEdge(t *testing.T) {
	f := NewFunc("loop")
	c := f.AddParam()
	entry := f.NewBlock()  // ID 1
	header := f.NewBlock() // ID 2
	body := f.NewBlock()   // ID 3
	done := f.NewBlock()   // ID 4
	f.SetBr(entry, header)
	f.SetBrIf(header, c, body, done)
	f.SetBr(body, header)
	f.SetRet(done, Value{})

	dot := f.ToDot()
	want := "block_3 -> block_2"
	if !strings.Contains(dot, want) {
		t.Errorf("DOT missing back-edge %q in:\n%s", want, dot)
	}
}
