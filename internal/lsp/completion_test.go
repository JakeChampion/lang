package lsp

import (
	"encoding/json"
	"strings"
	"testing"
)

func completionFor(src string, line, col int) *completionList {
	s := NewServer()
	s.updateDoc("file:///t", src)
	return runCompletion(s.docs["file:///t"], "file:///t", Position{Line: line, Character: col})
}

// labelsContain reports whether the completion list includes an item
// with the given label. Used in place of strict equality so adding
// keywords / builtins to the language doesn't churn the tests.
func labelsContain(items []completionItem, label string) bool {
	for _, it := range items {
		if it.Label == label {
			return true
		}
	}
	return false
}

// detailFor finds the first item with the given label and returns
// its Detail. Empty string when no match.
func detailFor(items []completionItem, label string) string {
	for _, it := range items {
		if it.Label == label {
			return it.Detail
		}
	}
	return ""
}

func TestCompletion_InFunctionBody(t *testing.T) {
	src := "function add(a: i32, b: i32): i32 {\n  var sum: i32 = a + b;\n  return sum;\n}\n"
	// Cursor inside the body, somewhere after `var sum...`.
	got := completionFor(src, 2, 2)
	if got == nil || len(got.Items) == 0 {
		t.Fatal("expected completion items")
	}
	if !labelsContain(got.Items, "sum") {
		t.Errorf("expected local `sum` in items")
	}
	if !labelsContain(got.Items, "a") || !labelsContain(got.Items, "b") {
		t.Errorf("expected parameters a, b in items")
	}
	if !labelsContain(got.Items, "add") {
		t.Errorf("expected top-level function `add`")
	}
	if !labelsContain(got.Items, "function") {
		t.Errorf("expected keyword `function`")
	}
	if got.IsIncomplete {
		t.Errorf("IsIncomplete should be false (full list returned)")
	}
}

func TestCompletion_LocalCarriesType(t *testing.T) {
	src := "function main(): i32 {\n  var n: i32 = 7;\n  return n;\n}\n"
	got := completionFor(src, 2, 2)
	if got == nil {
		t.Fatal("expected completion items")
	}
	detail := detailFor(got.Items, "n")
	if detail != "i32" {
		t.Errorf("local `n` detail = %q, want i32", detail)
	}
}

func TestCompletion_AtTopLevel(t *testing.T) {
	src := "function f(): i32 { return 0; }\n\nstruct Point { x: i32, y: i32 }\n"
	// Cursor on the blank line between decls — no enclosing function.
	got := completionFor(src, 1, 0)
	if got == nil {
		t.Fatal("expected completion items")
	}
	if !labelsContain(got.Items, "f") {
		t.Errorf("expected top-level `f` in items")
	}
	if !labelsContain(got.Items, "Point") {
		t.Errorf("expected struct `Point` in items")
	}
	if !labelsContain(got.Items, "struct") {
		t.Errorf("expected keyword `struct`")
	}
}

func TestCompletion_IncludesEnumVariants(t *testing.T) {
	src := "enum Color { Red, Green, Blue }\nfunction main(): i32 { return 0; }\n"
	got := completionFor(src, 1, 24)
	if got == nil {
		t.Fatal("expected completion items")
	}
	if !labelsContain(got.Items, "Color") {
		t.Errorf("expected enum `Color`")
	}
	for _, v := range []string{"Red", "Green", "Blue"} {
		if !labelsContain(got.Items, v) {
			t.Errorf("expected variant %q", v)
		}
	}
}

func TestCompletion_SortedByLabel(t *testing.T) {
	src := "function main(): i32 { return 0; }\n"
	got := completionFor(src, 0, 25)
	for i := 1; i < len(got.Items); i++ {
		if got.Items[i-1].Label > got.Items[i].Label {
			t.Fatalf("items not sorted: %q before %q", got.Items[i-1].Label, got.Items[i].Label)
		}
	}
}

func TestHandleMessage_Completion(t *testing.T) {
	s := NewServer()
	s.SetPublisher(func(string, any) {})
	open := jsonRaw(didOpenParams{
		TextDocument: textDocumentItem{
			URI:        "file:///c.fern",
			LanguageID: "fern",
			Text:       "function add(a: i32, b: i32): i32 {\n  return a + b;\n}\n",
		},
	})
	openMsg, _ := json.Marshal(message{Jsonrpc: "2.0", Method: "textDocument/didOpen", Params: open})
	s.HandleMessage(openMsg)

	cp := completionParams{}
	cp.TextDocument.URI = "file:///c.fern"
	cp.Position = Position{Line: 1, Character: 2}
	msg, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		ID:      json.RawMessage("11"),
		Method:  "textDocument/completion",
		Params:  jsonRaw(cp),
	})
	resp := s.HandleMessage(msg)
	var m message
	if err := json.Unmarshal(resp, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Error != nil {
		t.Fatalf("completion errored: %+v", m.Error)
	}
	var list completionList
	if err := json.Unmarshal(m.Result, &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if !strings.Contains(string(m.Result), `"a"`) {
		t.Errorf("expected parameter `a` in result, got %s", m.Result)
	}
	if len(list.Items) == 0 {
		t.Fatal("got 0 completion items")
	}
}
