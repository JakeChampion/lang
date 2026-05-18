package lsp

import (
	"testing"
)

func referencesFor(src string, line, col int) []Location {
	s := NewServer()
	s.updateDoc("file:///t", src)
	return runReferences(s.docs["file:///t"], "file:///t", Position{Line: line, Character: col})
}

func renameFor(src string, line, col int, newName string) *workspaceEdit {
	s := NewServer()
	s.updateDoc("file:///t", src)
	return runRename(s.docs["file:///t"], "file:///t", Position{Line: line, Character: col}, newName)
}

func TestReferences_LocalVar(t *testing.T) {
	// `var x = 1; return x + x;` — three occurrences (decl + 2 uses).
	src := "function main(): i32 {\n  var x = 1;\n  return x + x;\n}\n"
	got := referencesFor(src, 2, 9) // cursor on first `x` use
	if len(got) != 3 {
		t.Errorf("expected 3 occurrences of x, got %d (%+v)", len(got), got)
	}
}

func TestReferences_TopLevelFunction(t *testing.T) {
	src := "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, add(2, 3)); }\n"
	// Cursor on the first `add` call in main — call sites are
	// real *ast.Ident nodes; decl-site name positions don't
	// surface a node (FuncDecl.Name is a string).
	got := referencesFor(src, 1, 31)
	if len(got) < 3 {
		t.Errorf("expected at least 3 occurrences of add (decl + 2 calls), got %d (%+v)", len(got), got)
	}
}

func TestReferences_NoneAtBlankSpot(t *testing.T) {
	src := "function main(): i32 { return 0; }\n"
	got := referencesFor(src, 0, 25) // somewhere in `return` whitespace
	// 'r' of return matches an ident? Let's pick an actual blank
	// spot — on the `0` literal.
	got = referencesFor(src, 0, 30) // on the `0`
	if len(got) != 0 {
		t.Errorf("expected 0 occurrences on a NumberLit, got %d (%+v)", len(got), got)
	}
}

func TestRename_LocalVar(t *testing.T) {
	src := "function main(): i32 {\n  var x = 1;\n  return x + x;\n}\n"
	got := renameFor(src, 2, 9, "renamed")
	if got == nil {
		t.Fatal("expected a WorkspaceEdit, got nil")
	}
	edits := got.Changes["file:///t"]
	if len(edits) != 3 {
		t.Errorf("expected 3 text edits, got %d (%+v)", len(edits), edits)
	}
	for _, e := range edits {
		if e.NewText != "renamed" {
			t.Errorf("edit newText = %q, want \"renamed\"", e.NewText)
		}
	}
}

func TestRename_TopLevelFunction(t *testing.T) {
	src := "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n"
	// Cursor on the call-site `add` in main.
	got := renameFor(src, 1, 31, "plus")
	if got == nil {
		t.Fatal("expected WorkspaceEdit, got nil")
	}
	if len(got.Changes["file:///t"]) < 2 {
		t.Errorf("expected at least 2 edits (decl + call), got %d", len(got.Changes["file:///t"]))
	}
}

func TestRename_OnMethodCallIsNoOp(t *testing.T) {
	// Method renames need Methods-map rewriting we don't support
	// yet — return nil to signal "rename not available here".
	src := "struct Point { x: i32, y: i32 }\n" +
		"function (p: Point) sum(): i32 { return p.x + p.y; }\n" +
		"function main(): i32 {\n  var p: Point = Point { x: 1, y: 2 };\n  return p.sum();\n}\n"
	got := renameFor(src, 4, 13, "total")
	if got != nil {
		t.Errorf("expected nil for method-call rename, got %+v", got)
	}
}
