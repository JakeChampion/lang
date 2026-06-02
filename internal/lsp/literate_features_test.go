package lsp

import (
	"encoding/json"
	"testing"
)

// openLiterate loads a .fern.md document into a fresh server.
func openLiterate(t *testing.T, src string) *Server {
	t.Helper()
	s := NewServer()
	s.updateDoc("file:///t.fern.md", src)
	return s
}

// posReq marshals a {textDocument, position} request body.
func posReq(t *testing.T, line, char int) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"textDocument": map[string]string{"uri": "file:///t.fern.md"},
		"position":     map[string]int{"line": line, "character": char},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A document whose root chunk defines `greeting` (line 3) and calls it
// from `main` (line 4). Tangling drops nothing and adds no indentation,
// so document lines 3/4 map straight to generated lines 1/2.
const litFeatureDoc = "```fern\n" + // line 1
	"<<*>>=\n" + // line 2
	"function greeting(): i32 { return 7; }\n" + // line 3
	"function main(): i32 { return greeting(); }\n" + // line 4
	"```\n" // line 5

// Go-to-definition from the call site lands on the definition's
// *document* line, not the tangled line.
func TestLiterateLSP_Definition(t *testing.T) {
	s := openLiterate(t, litFeatureDoc)
	// `greeting` in the call on document line 4 (0-based 3), col ~33.
	res, rerr := s.handleDefinition(posReq(t, 3, 33))
	if rerr != nil {
		t.Fatalf("definition: %v", rerr)
	}
	loc, ok := res.(*Location)
	if !ok || loc == nil {
		t.Fatalf("expected a definition Location, got %T %v", res, res)
	}
	if loc.URI != "file:///t.fern.md" {
		t.Errorf("definition URI = %q, want the document", loc.URI)
	}
	if loc.Range.Start.Line != 2 {
		t.Errorf("definition remapped to line %d, want 2 (document line 3)", loc.Range.Start.Line)
	}
}

// Hover on the definition resolves and reports a range on the document
// line.
func TestLiterateLSP_Hover(t *testing.T) {
	s := openLiterate(t, litFeatureDoc)
	// `greeting` at the call on document line 4 (0-based 3), col ~33 —
	// findNameAt matches use-site idents, not declaration tokens.
	res, rerr := s.handleHover(posReq(t, 3, 33))
	if rerr != nil {
		t.Fatalf("hover: %v", rerr)
	}
	if res == nil {
		t.Fatal("expected a hover result")
	}
	h, ok := res.(*hoverResult)
	if !ok {
		t.Fatalf("expected *hoverResult, got %T", res)
	}
	// The hovered token is on document line 4 (0-based 3), not the
	// tangled line (1) — i.e. the range was remapped.
	if h.Range != nil && h.Range.Start.Line != 3 {
		t.Errorf("hover range remapped to line %d, want 3", h.Range.Start.Line)
	}
}

// Find-all-references returns both the definition and the call, each on
// its document line.
func TestLiterateLSP_References(t *testing.T) {
	s := openLiterate(t, litFeatureDoc)
	// Query at the call on document line 4 (0-based 3); references
	// returns the declaration + the call.
	res, rerr := s.handleReferences(posReq(t, 3, 33))
	if rerr != nil {
		t.Fatalf("references: %v", rerr)
	}
	locs, ok := res.([]Location)
	if !ok || len(locs) < 2 {
		t.Fatalf("expected >= 2 reference locations, got %T %v", res, res)
	}
	lines := map[int]bool{}
	for _, l := range locs {
		if l.URI != "file:///t.fern.md" {
			t.Errorf("reference URI = %q, want the document", l.URI)
		}
		lines[l.Range.Start.Line] = true
	}
	if !lines[2] || !lines[3] {
		t.Errorf("references should cover document lines 3 and 4 (0-based 2,3), got %v", lines)
	}
}

// Completion at the call site offers the in-scope `greeting`.
func TestLiterateLSP_Completion(t *testing.T) {
	s := openLiterate(t, litFeatureDoc)
	res, rerr := s.handleCompletion(posReq(t, 3, 33))
	if rerr != nil {
		t.Fatalf("completion: %v", rerr)
	}
	cl, ok := res.(*completionList)
	if !ok || cl == nil {
		t.Fatalf("expected a completionList, got %T", res)
	}
	found := false
	for _, it := range cl.Items {
		if it.Label == "greeting" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("completion should offer `greeting`, got %d items", len(cl.Items))
	}
}

// A cursor on a non-tangled line (prose / the chunk header) yields no
// result rather than querying a bogus generated position.
func TestLiterateLSP_NonTangledLineInert(t *testing.T) {
	s := openLiterate(t, litFeatureDoc)
	// Line 2 (0-based 1) is the `<<*>>=` header — not in the tangle.
	if res, _ := s.handleHover(posReq(t, 1, 2)); res != nil {
		t.Errorf("hover on the chunk header should be nil, got %v", res)
	}
}
