package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a tiny test helper — keeps each workspace fixture
// terse instead of repeating the os.WriteFile boilerplate.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestWorkspace_CrossModuleStructDefinition(t *testing.T) {
	// Set up a two-file workspace on disk:
	//   util.lang  — declares struct Point
	//   main.lang  — imports ./util, uses util.Point in a function
	//                signature; cursor on `Point` should jump to
	//                util.lang.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "util.lang"),
		"pub struct Point { x: i32, y: i32 }\n")
	mainPath := filepath.Join(dir, "main.lang")
	mainSrc := "import \"./util\";\n" +
		"function origin(): util.Point { return util.Point { x: 0, y: 0 }; }\n" +
		"function main(): i32 { var p: util.Point = origin(); return p.x; }\n"
	writeFile(t, mainPath, mainSrc)

	s := NewServer()
	s.EnableWorkspace()
	s.SetPublisher(func(string, any) {})

	mainURI := pathToURI(mainPath)
	open, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		Method:  "textDocument/didOpen",
		Params: jsonRaw(didOpenParams{
			TextDocument: textDocumentItem{
				URI:        mainURI,
				LanguageID: "lang",
				Text:       mainSrc,
			},
		}),
	})
	s.HandleMessage(open)

	// `function main(): i32 { var p: util.Point = ...` — `util`
	// starts at 1-based col 31, so the TypeRef spans cols [31, 41)
	// for the spelling "util.Point". Cursor at LSP (line 2, col 35)
	// = 1-based (line 3, col 36) is on the `P` of `Point`.
	hit := findNameAt(s.docs[mainURI].prog, 3, 36)
	if hit == nil || hit.typeRef == nil {
		t.Fatalf("expected a typeRef hit inside `util.Point`, got %+v", hit)
	}

	dp := definitionParams{}
	dp.TextDocument.URI = mainURI
	dp.Position = Position{Line: 2, Character: 35} // inside `util.Point`
	defMsg, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		ID:      json.RawMessage("1"),
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
	if len(m.Result) == 0 || string(m.Result) == "null" {
		t.Fatalf("expected a Location, got null. Hit details: %+v", hit)
	}
	var loc Location
	if err := json.Unmarshal(m.Result, &loc); err != nil {
		t.Fatalf("unmarshal location: %v", err)
	}
	// The struct Point lives in util.lang — definition should jump
	// there, not echo back the main.lang URI.
	if !strings.HasSuffix(loc.URI, "util.lang") {
		t.Errorf("definition uri = %q, want a util.lang URI", loc.URI)
	}
	if loc.Range.Start.Line != 0 {
		t.Errorf("definition start line = %d, want 0 (struct Point in util.lang)", loc.Range.Start.Line)
	}
}

func TestWorkspace_UnsavedBufferOverridesDisk(t *testing.T) {
	// The point of LoadWith: the editor's in-memory buffer wins
	// over what's on disk for an open file. Write a STALE version
	// to disk; open the file in the LSP with the FRESH version;
	// verify the fresh version drives diagnostics.
	dir := t.TempDir()
	stale := filepath.Join(dir, "main.lang")
	writeFile(t, stale, "function main(): i32 { return undeclared_in_stale; }\n")
	freshSrc := "function main(): i32 { return 0; }\n"

	s := NewServer()
	s.EnableWorkspace()
	var lastDiagCount int
	s.SetPublisher(func(method string, params any) {
		if method != "textDocument/publishDiagnostics" {
			return
		}
		b, _ := json.Marshal(params)
		var p publishDiagnosticsParams
		_ = json.Unmarshal(b, &p)
		lastDiagCount = len(p.Diagnostics)
	})

	open, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		Method:  "textDocument/didOpen",
		Params: jsonRaw(didOpenParams{
			TextDocument: textDocumentItem{
				URI:        pathToURI(stale),
				LanguageID: "lang",
				Text:       freshSrc,
			},
		}),
	})
	s.HandleMessage(open)

	if lastDiagCount != 0 {
		t.Errorf("expected zero diagnostics from fresh buffer, got %d (probably read stale disk content)", lastDiagCount)
	}
}

func TestUriToPath_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.lang")
	uri := pathToURI(path)
	got, ok := uriToPath(uri)
	if !ok {
		t.Fatalf("uriToPath(%q) returned !ok", uri)
	}
	if got != path {
		t.Errorf("round-trip path = %q, want %q", got, path)
	}
}

func TestUriToPath_RejectsNonFileScheme(t *testing.T) {
	if _, ok := uriToPath("inmemory:///x.lang"); ok {
		t.Errorf("expected uriToPath to reject non-file scheme")
	}
	if _, ok := uriToPath("https://example.com/x.lang"); ok {
		t.Errorf("expected uriToPath to reject http scheme")
	}
}
