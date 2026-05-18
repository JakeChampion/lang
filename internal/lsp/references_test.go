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

func TestRename_MethodCall(t *testing.T) {
	src := "struct Point { x: i32, y: i32 }\n" +
		"function (p: Point) sum(): i32 { return p.x + p.y; }\n" +
		"function main(): i32 {\n  var p: Point = Point { x: 1, y: 2 };\n  return p.sum();\n}\n"
	got := renameFor(src, 4, 13, "total")
	if got == nil {
		t.Fatal("expected a WorkspaceEdit for method rename, got nil")
	}
	edits := got.Changes["file:///t"]
	if len(edits) != 2 {
		t.Errorf("expected 2 edits (decl + 1 call site), got %d (%+v)", len(edits), edits)
	}
	for _, e := range edits {
		if e.NewText != "total" {
			t.Errorf("edit newText = %q, want \"total\"", e.NewText)
		}
	}
}

func TestRename_StructField(t *testing.T) {
	src := "struct Point { x: i32, y: i32 }\n" +
		"function main(): i32 {\n  var p: Point = Point { x: 3, y: 4 };\n  return p.x;\n}\n"
	got := renameFor(src, 3, 11, "horiz") // cursor on `x` in `p.x`
	if got == nil {
		t.Fatal("expected a WorkspaceEdit for field rename, got nil")
	}
	edits := got.Changes["file:///t"]
	// decl-site `x` + struct-lit `x:` + access `p.x` = 3 edits.
	if len(edits) != 3 {
		t.Errorf("expected 3 edits (decl + struct lit + access), got %d (%+v)", len(edits), edits)
	}
}

func TestRename_EnumVariant(t *testing.T) {
	src := "enum Color { Red, Green, Blue }\n" +
		"function main(): i32 {\n  var c: Color = Red;\n  match (c) { Red => { return 1; }, _ => { return 0; } }\n}\n"
	got := renameFor(src, 2, 17, "Crimson") // cursor on `Red` in init
	if got == nil {
		t.Fatal("expected a WorkspaceEdit for variant rename, got nil")
	}
	edits := got.Changes["file:///t"]
	// decl + literal + match arm = 3 edits.
	if len(edits) != 3 {
		t.Errorf("expected 3 edits (decl + literal + match arm), got %d (%+v)", len(edits), edits)
	}
}
