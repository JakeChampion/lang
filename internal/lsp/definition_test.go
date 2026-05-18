package lsp

import (
	"encoding/json"
	"testing"
)

func definitionFor(src string, line, col int) *Location {
	s := NewServer()
	s.updateDoc("file:///t", src)
	return runDefinition(s.docs["file:///t"], "file:///t", Position{Line: line, Character: col})
}

func TestDefinition_LocalVar(t *testing.T) {
	src := "function main(): i32 {\n  var x: i32 = 7;\n  return x;\n}\n"
	// Cursor on `x` in `return x;` (LSP 0-based line 2, col 9).
	// The parser stamps Var.P at the start of the `var` keyword
	// (LSP col 2), not at the name — close enough to navigate.
	got := definitionFor(src, 2, 9)
	if got == nil {
		t.Fatal("expected definition for local var x, got nil")
	}
	if got.Range.Start.Line != 1 {
		t.Errorf("definition start line = %d, want 1", got.Range.Start.Line)
	}
	if got.Range.Start.Character != 2 {
		t.Errorf("definition start col = %d, want 2 (start of `var` keyword)", got.Range.Start.Character)
	}
}

func TestDefinition_FunctionCall(t *testing.T) {
	src := "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n"
	// Cursor on `add` in `return add(1, 2)` — line 1, col 31.
	got := definitionFor(src, 1, 31)
	if got == nil {
		t.Fatal("expected definition for add(), got nil")
	}
	if got.Range.Start.Line != 0 {
		t.Errorf("definition start line = %d, want 0 (first line)", got.Range.Start.Line)
	}
}

func TestDefinition_Struct(t *testing.T) {
	src := "struct Point { x: i32, y: i32 }\nfunction origin(): Point { return Point { x: 0, y: 0 }; }\n"
	// Cursor on `Point` in `return Point { ... }` — line 1, col 34.
	got := definitionFor(src, 1, 34)
	if got == nil {
		t.Fatal("expected definition for struct Point")
	}
	if got.Range.Start.Line != 0 {
		t.Errorf("definition start line = %d, want 0", got.Range.Start.Line)
	}
}

func TestDefinition_TypeAnnotation(t *testing.T) {
	// Cursor on `Point` in `var p: Point`. The annotation lives in
	// a positionless ast.Type, so this only works via the parser's
	// TypeRefs side table.
	src := "struct Point { x: i32, y: i32 }\nfunction main(): i32 {\n  var p: Point = Point { x: 0, y: 0 };\n  return p.x;\n}\n"
	got := definitionFor(src, 2, 9)
	if got == nil {
		t.Fatal("expected definition for type annotation Point")
	}
	if got.Range.Start.Line != 0 {
		t.Errorf("definition start line = %d, want 0 (struct decl)", got.Range.Start.Line)
	}
}

func TestDefinition_FieldAccess(t *testing.T) {
	src := "struct Point { x: i32, y: i32 }\nfunction main(): i32 {\n  var p: Point = Point { x: 7, y: 9 };\n  return p.x;\n}\n"
	got := definitionFor(src, 3, 11) // cursor on `x` in `p.x`
	if got == nil {
		t.Fatal("expected definition for field access p.x")
	}
	// Jumps to the struct decl (line 0) — StructDecl.Fields lack
	// per-field positions, so the LSP picks the decl's start as a
	// reasonable target.
	if got.Range.Start.Line != 0 {
		t.Errorf("definition start line = %d, want 0 (struct decl)", got.Range.Start.Line)
	}
}

func TestDefinition_NoIdentAtPosition(t *testing.T) {
	src := "function main(): i32 { return 0; }\n"
	got := definitionFor(src, 0, 30)
	if got != nil {
		t.Errorf("expected nil definition, got %+v", got)
	}
}

func TestDefinition_UnknownName(t *testing.T) {
	// `undeclared_xyz` doesn't resolve; the source still parses, so
	// findIdentAt sees the Ident but locateDefinition rejects it.
	src := "function main(): i32 { return undeclared_xyz; }\n"
	got := definitionFor(src, 0, 32)
	if got != nil {
		t.Errorf("expected nil definition for unknown name, got %+v", got)
	}
}

func TestHandleMessage_Definition(t *testing.T) {
	s := NewServer()
	s.SetPublisher(func(string, any) {})
	open := jsonRaw(didOpenParams{
		TextDocument: textDocumentItem{
			URI:        "file:///def.lang",
			LanguageID: "lang",
			Text:       "function main(): i32 {\n  var x: i32 = 7;\n  return x;\n}\n",
		},
	})
	openMsg, _ := json.Marshal(message{Jsonrpc: "2.0", Method: "textDocument/didOpen", Params: open})
	s.HandleMessage(openMsg)

	dp := definitionParams{}
	dp.TextDocument.URI = "file:///def.lang"
	dp.Position = Position{Line: 2, Character: 9}
	defMsg, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		ID:      json.RawMessage("9"),
		Method:  "textDocument/definition",
		Params:  jsonRaw(dp),
	})
	resp := s.HandleMessage(defMsg)
	var m message
	if err := json.Unmarshal(resp, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Error != nil {
		t.Fatalf("definition errored: %+v", m.Error)
	}
	var loc Location
	if err := json.Unmarshal(m.Result, &loc); err != nil {
		t.Fatalf("unmarshal location: %v", err)
	}
	if loc.URI != "file:///def.lang" {
		t.Errorf("location uri = %q", loc.URI)
	}
	if loc.Range.Start.Line != 1 {
		t.Errorf("location line = %d, want 1", loc.Range.Start.Line)
	}
}
