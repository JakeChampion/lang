package lsp

import (
	"encoding/json"
	"strings"
	"testing"
)

// hoverFor is a test helper that builds a Server, opens the document,
// and returns the hover result for the given (0-based) LSP position.
// Nil result means "no hover at that spot" (per LSP semantics).
func hoverFor(src string, line, col int) *hoverResult {
	s := NewServer()
	s.updateDoc("file:///t", src)
	return runHover(s.docs["file:///t"], "file:///t", Position{Line: line, Character: col})
}

func TestHover_LocalVar(t *testing.T) {
	src := "function main(): i32 {\n  var x: i32 = 7;\n  return x;\n}\n"
	// Cursor on the `x` in `return x;` — line index 2, column index 9.
	got := hoverFor(src, 2, 9)
	if got == nil {
		t.Fatal("expected hover for local var x, got nil")
	}
	if !strings.Contains(got.Contents.Value, "(var) x: i32") {
		t.Errorf("hover content = %q, want it to mention (var) x: i32", got.Contents.Value)
	}
	if got.Range == nil {
		t.Errorf("hover should set a range")
	}
}

func TestHover_Parameter(t *testing.T) {
	src := "function add(a: i32, b: i32): i32 {\n  return a + b;\n}\n"
	// Cursor on `a` in `return a + b;` — line 1, column 9.
	got := hoverFor(src, 1, 9)
	if got == nil {
		t.Fatal("expected hover for parameter a")
	}
	if !strings.Contains(got.Contents.Value, "(parameter) a: i32") {
		t.Errorf("hover = %q, want (parameter) a: i32", got.Contents.Value)
	}
}

func TestHover_FunctionCall(t *testing.T) {
	src := "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n"
	// Cursor on `add` inside `return add(1, 2);` — line 1, column 31.
	got := hoverFor(src, 1, 31)
	if got == nil {
		t.Fatal("expected hover for function call")
	}
	if !strings.Contains(got.Contents.Value, "function add(i32, i32): i32") {
		t.Errorf("hover = %q, want function signature", got.Contents.Value)
	}
}

func TestHover_StructType(t *testing.T) {
	src := "struct Point { x: i32, y: i32 }\nfunction origin(): Point { return Point { x: 0, y: 0 }; }\n"
	// Cursor on `Point` in `return Point { ... }` — line 1, column 34.
	got := hoverFor(src, 1, 34)
	if got == nil {
		t.Fatal("expected hover for struct constructor")
	}
	if !strings.Contains(got.Contents.Value, "struct Point") {
		t.Errorf("hover = %q, want struct Point", got.Contents.Value)
	}
	if !strings.Contains(got.Contents.Value, "x: i32") {
		t.Errorf("hover = %q, want field list", got.Contents.Value)
	}
}

func TestHover_TypeAnnotation_Struct(t *testing.T) {
	// Cursor on `Point` in `var p: Point` — that name lives in a
	// positionless ast.Type, so resolving it relies on the parser's
	// TypeRefs side table.
	src := "struct Point { x: i32, y: i32 }\nfunction main(): i32 {\n  var p: Point = Point { x: 0, y: 0 };\n  return p.x;\n}\n"
	got := hoverFor(src, 2, 9) // `P` of `Point` in the annotation
	if got == nil {
		t.Fatal("expected hover for type annotation Point")
	}
	if !strings.Contains(got.Contents.Value, "struct Point") {
		t.Errorf("hover = %q, want it to describe struct Point", got.Contents.Value)
	}
}

func TestHover_TypeAnnotation_Enum(t *testing.T) {
	src := "enum Color { Red, Green, Blue }\nfunction main(): i32 {\n  var c: Color = Red;\n  return 0;\n}\n"
	got := hoverFor(src, 2, 9) // `C` of `Color` in the annotation
	if got == nil {
		t.Fatal("expected hover for type annotation Color")
	}
	if !strings.Contains(got.Contents.Value, "enum Color") {
		t.Errorf("hover = %q, want enum Color description", got.Contents.Value)
	}
}

func TestHover_FieldAccess(t *testing.T) {
	src := "struct Point { x: i32, y: i32 }\nfunction main(): i32 {\n  var p: Point = Point { x: 7, y: 9 };\n  return p.x;\n}\n"
	got := hoverFor(src, 3, 11) // cursor on `x` in `p.x`
	if got == nil {
		t.Fatal("expected hover for field access p.x")
	}
	if !strings.Contains(got.Contents.Value, "(field) x: i32") {
		t.Errorf("hover = %q, want (field) x: i32", got.Contents.Value)
	}
}

func TestHover_MethodCall(t *testing.T) {
	src := "struct Point { x: i32, y: i32 }\n" +
		"function (p: Point) sum(): i32 { return p.x + p.y; }\n" +
		"function main(): i32 {\n  var p: Point = Point { x: 3, y: 4 };\n  return p.sum();\n}\n"
	// Cursor on `sum` in `p.sum()` — line 4 (0-based), col 13.
	// `  return p.sum();` → `s` of `sum` is at col 13 (0-based).
	got := hoverFor(src, 4, 13)
	if got == nil {
		t.Fatal("expected hover for method call p.sum()")
	}
	if !strings.Contains(got.Contents.Value, "sum") {
		t.Errorf("hover = %q, want it to mention sum", got.Contents.Value)
	}
	if !strings.Contains(got.Contents.Value, "Point") {
		t.Errorf("hover = %q, want it to mention Point (receiver)", got.Contents.Value)
	}
	if !strings.Contains(got.Contents.Value, "i32") {
		t.Errorf("hover = %q, want it to mention the i32 return", got.Contents.Value)
	}
}

func TestHover_FieldAccess_Chained(t *testing.T) {
	src := "struct Inner { v: i32 }\nstruct Outer { inner: Inner }\n" +
		"function main(): i32 {\n  var o: Outer = Outer { inner: Inner { v: 5 } };\n  return o.inner.v;\n}\n"
	// Cursor on `v` in `o.inner.v` — chained access requires
	// resolving o → Outer.inner → Inner.v.
	got := hoverFor(src, 4, 17)
	if got == nil {
		t.Fatal("expected hover for chained field access")
	}
	if !strings.Contains(got.Contents.Value, "(field) v: i32") {
		t.Errorf("hover = %q, want (field) v: i32", got.Contents.Value)
	}
}

func TestHover_EnumVariant(t *testing.T) {
	src := "enum Color { Red, Green, Blue }\nfunction main(): i32 { var c: Color = Red; return 0; }\n"
	// Cursor on `Red` in `var c: Color = Red;` — line 1, col 39.
	// Type-annotation positions (`Color`) aren't recognised yet —
	// they're stored as positionless ast.Types — but enum variants
	// have an EnumLit node we can locate.
	got := hoverFor(src, 1, 39)
	if got == nil {
		t.Fatal("expected hover for enum variant Red")
	}
	if !strings.Contains(got.Contents.Value, "Red") {
		t.Errorf("hover = %q, want it to mention Red", got.Contents.Value)
	}
	if !strings.Contains(got.Contents.Value, "Color") {
		t.Errorf("hover = %q, want it to attribute the variant to Color", got.Contents.Value)
	}
}

func TestHover_NoIdentAtPosition(t *testing.T) {
	src := "function main(): i32 { return 0; }\n"
	// Cursor on the literal `0` — there's no Ident there.
	got := hoverFor(src, 0, 30)
	if got != nil {
		t.Errorf("expected nil hover on a NumberLit, got %+v", got)
	}
}

func TestHover_OnWhitespace(t *testing.T) {
	src := "function main(): i32 {\n   \n  return 0;\n}\n"
	// Empty line 1, column 1.
	got := hoverFor(src, 1, 1)
	if got != nil {
		t.Errorf("expected nil hover on whitespace, got %+v", got)
	}
}

func TestHover_LocalShadowsParameter(t *testing.T) {
	src := "function f(x: i32): i32 {\n  var x: string = \"hi\";\n  return x.len();\n}\n"
	// Cursor on the `x` in `x.len()` — should resolve to the
	// local (string), not the parameter (i32).
	got := hoverFor(src, 2, 9)
	if got == nil {
		t.Fatal("expected hover for shadowed local")
	}
	if !strings.Contains(got.Contents.Value, "(var) x: string") {
		t.Errorf("hover = %q, want local string to win over parameter i32", got.Contents.Value)
	}
}

func TestHandleMessage_Hover(t *testing.T) {
	s := NewServer()
	s.SetPublisher(func(string, any) {})
	// Open a document.
	open := jsonRaw(didOpenParams{
		TextDocument: textDocumentItem{
			URI:        "file:///hover.fern",
			LanguageID: "fern",
			Text:       "function main(): i32 {\n  var x: i32 = 7;\n  return x;\n}\n",
		},
	})
	openMsg, _ := json.Marshal(message{Jsonrpc: "2.0", Method: "textDocument/didOpen", Params: open})
	s.HandleMessage(openMsg)

	// Hover on the `x` in `return x;`.
	hp := hoverParams{}
	hp.TextDocument.URI = "file:///hover.fern"
	hp.Position = Position{Line: 2, Character: 9}
	hoverMsg, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		ID:      json.RawMessage("3"),
		Method:  "textDocument/hover",
		Params:  jsonRaw(hp),
	})
	resp := s.HandleMessage(hoverMsg)
	var m message
	if err := json.Unmarshal(resp, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Error != nil {
		t.Fatalf("hover errored: %+v", m.Error)
	}
	if len(m.Result) == 0 || string(m.Result) == "null" {
		t.Fatalf("expected hover result, got null")
	}
	var hr hoverResult
	if err := json.Unmarshal(m.Result, &hr); err != nil {
		t.Fatalf("unmarshal hover result: %v", err)
	}
	if !strings.Contains(hr.Contents.Value, "(var) x: i32") {
		t.Errorf("hover content = %q", hr.Contents.Value)
	}
}
