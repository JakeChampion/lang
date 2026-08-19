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
	//   util.fern  — declares struct Point
	//   main.fern  — imports ./util, uses util.Point in a function
	//                signature; cursor on `Point` should jump to
	//                util.fern.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "util.fern"),
		"pub struct Point { x: i32, y: i32 }\n")
	mainPath := filepath.Join(dir, "main.fern")
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
				LanguageID: "fern",
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
	// The struct Point lives in util.fern — definition should jump
	// there, not echo back the main.fern URI.
	if !strings.HasSuffix(loc.URI, "util.fern") {
		t.Errorf("definition uri = %q, want a util.fern URI", loc.URI)
	}
	if loc.Range.Start.Line != 0 {
		t.Errorf("definition start line = %d, want 0 (struct Point in util.fern)", loc.Range.Start.Line)
	}
}

func TestWorkspace_CrossModuleCallJump(t *testing.T) {
	// `util.foo()` cursor on `foo` should jump to util.fern at the
	// declaration of `foo`. modload preserves the FieldPos on
	// Call.Module so the LSP can locate it after the rewrite.
	dir := t.TempDir()
	utilPath := filepath.Join(dir, "util.fern")
	mainPath := filepath.Join(dir, "main.fern")
	writeFile(t, utilPath, "pub function foo(): i32 { return 42; }\n")
	mainSrc := "import \"./util\";\nfunction main(): i32 { return util.foo(); }\n"
	writeFile(t, mainPath, mainSrc)

	s := NewServer()
	s.EnableWorkspace()
	s.SetPublisher(func(string, any) {})

	mainURI := pathToURI(mainPath)
	open, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		Method:  "textDocument/didOpen",
		Params: jsonRaw(didOpenParams{TextDocument: textDocumentItem{
			URI: mainURI, LanguageID: "fern", Text: mainSrc,
		}}),
	})
	s.HandleMessage(open)

	// `function main(): i32 { return util.foo(); }`
	// 0-based cols: `util` starts at 30, `.` at 34, `foo` at 35.
	// Cursor on `f` of `foo`.
	dp := definitionParams{}
	dp.TextDocument.URI = mainURI
	dp.Position = Position{Line: 1, Character: 35}
	msg, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "textDocument/definition",
		Params:  jsonRaw(dp),
	})
	resp := s.HandleMessage(msg)
	var m message
	if err := json.Unmarshal(resp, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.Result) == 0 || string(m.Result) == "null" {
		t.Fatalf("expected a Location, got %s", m.Result)
	}
	var loc Location
	if err := json.Unmarshal(m.Result, &loc); err != nil {
		t.Fatalf("unmarshal location: %v", err)
	}
	if !strings.HasSuffix(loc.URI, "util.fern") {
		t.Errorf("definition uri = %q, want a util.fern URI", loc.URI)
	}
	if loc.Range.Start.Line != 0 {
		t.Errorf("definition start line = %d, want 0 (foo in util.fern)", loc.Range.Start.Line)
	}
}

func TestWorkspace_UnsavedBufferOverridesDisk(t *testing.T) {
	// The point of LoadWith: the editor's in-memory buffer wins
	// over what's on disk for an open file. Write a STALE version
	// to disk; open the file in the LSP with the FRESH version;
	// verify the fresh version drives diagnostics.
	dir := t.TempDir()
	stale := filepath.Join(dir, "main.fern")
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
				LanguageID: "fern",
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
	path := filepath.Join(dir, "x.fern")
	uri := pathToURI(path)
	got, ok := uriToPath(uri)
	if !ok {
		t.Fatalf("uriToPath(%q) returned !ok", uri)
	}
	if got != path {
		t.Errorf("round-trip path = %q, want %q", got, path)
	}
}

func TestWorkspace_CrossFileDiagnosticsRoute(t *testing.T) {
	// Two-file workspace: main.fern is clean; util.fern has a type
	// error. Both are open in the editor. After opening main, the
	// workspace load should publish empty diagnostics for main and
	// the util-side error for util.
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.fern")
	utilPath := filepath.Join(dir, "util.fern")
	writeFile(t, mainPath,
		"import \"./util\";\nfunction main(): i32 { return util.thing(); }\n")
	writeFile(t, utilPath,
		"pub function thing(): i32 { return undeclared_in_util; }\n")

	s := NewServer()
	s.EnableWorkspace()
	publishes := map[string][]Diagnostic{}
	s.SetPublisher(func(method string, params any) {
		if method != "textDocument/publishDiagnostics" {
			return
		}
		b, _ := json.Marshal(params)
		var p publishDiagnosticsParams
		_ = json.Unmarshal(b, &p)
		publishes[p.URI] = p.Diagnostics
	})

	mainURI := pathToURI(mainPath)
	utilURI := pathToURI(utilPath)

	// Open util first so it's in the override map; opening main
	// triggers the workspace load and should route util's errors
	// to its own URI.
	openUtil, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		Method:  "textDocument/didOpen",
		Params: jsonRaw(didOpenParams{TextDocument: textDocumentItem{
			URI: utilURI, LanguageID: "fern",
			Text: "pub function thing(): i32 { return undeclared_in_util; }\n",
		}}),
	})
	s.HandleMessage(openUtil)
	// util opened standalone — its own load reports the error.
	if len(publishes[utilURI]) == 0 {
		t.Fatalf("expected diagnostics on util URI after standalone open, got %+v", publishes[utilURI])
	}

	openMain, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		Method:  "textDocument/didOpen",
		Params: jsonRaw(didOpenParams{TextDocument: textDocumentItem{
			URI: mainURI, LanguageID: "fern",
			Text: "import \"./util\";\nfunction main(): i32 { return util.thing(); }\n",
		}}),
	})
	s.HandleMessage(openMain)

	// main should publish empty diagnostics (it's clean).
	if mainDiags := publishes[mainURI]; len(mainDiags) != 0 {
		t.Errorf("main URI should have no diagnostics, got %+v", mainDiags)
	}
	// util's diagnostics should STILL be populated — routed by file
	// path, not by which URI the editor opened.
	utilDiags := publishes[utilURI]
	if len(utilDiags) == 0 {
		t.Errorf("util URI should retain its diagnostics after main load, got empty")
	}
}

// A SYNTAX error in an imported module has to route the same way a type error
// does. It did not: the checker stamps its own file path on every diagnostic,
// while a parser error gets its path from modload's diag.WithFile call — and
// that stamp reached nothing, so every imported module's syntax error arrived
// unattributed and was published against the ENTRY file's URI, underlining
// whatever happened to be at that line:column in a file that parses fine.
//
// TestWorkspace_CrossFileDiagnosticsRoute cannot see this: a type error is
// stamped by the checker and never travelled the broken path.
func TestWorkspace_ImportedSyntaxErrorDoesNotLandOnTheEntry(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.fern")
	utilPath := filepath.Join(dir, "util.fern")
	const mainSrc = "import \"./util\";\nfunction main(): i32 { return util.thing(); }\n"
	const utilSrc = "pub function thing(): i32 {\n    return 1 +;\n}\n"
	writeFile(t, mainPath, mainSrc)
	writeFile(t, utilPath, utilSrc)

	s := NewServer()
	s.EnableWorkspace()
	publishes := map[string][]Diagnostic{}
	s.SetPublisher(func(method string, params any) {
		if method != "textDocument/publishDiagnostics" {
			return
		}
		b, _ := json.Marshal(params)
		var p publishDiagnosticsParams
		_ = json.Unmarshal(b, &p)
		publishes[p.URI] = p.Diagnostics
	})

	open := func(path, src string) {
		raw, _ := json.Marshal(message{
			Jsonrpc: "2.0",
			Method:  "textDocument/didOpen",
			Params: jsonRaw(didOpenParams{TextDocument: textDocumentItem{
				URI: pathToURI(path), LanguageID: "fern", Text: src,
			}}),
		})
		s.HandleMessage(raw)
	}
	// util first, so the workspace load triggered by opening main can route
	// back to an already-open URI (diagnostics are only published for files
	// the editor has open).
	open(utilPath, utilSrc)
	open(mainPath, mainSrc)

	if got := publishes[pathToURI(mainPath)]; len(got) != 0 {
		t.Errorf("main.fern parses cleanly; its URI must carry none of util.fern's syntax errors, got %+v", got)
	}
	utilDiags := publishes[pathToURI(utilPath)]
	if len(utilDiags) == 0 {
		t.Fatalf("util.fern lost its syntax error, publishes: %+v", publishes)
	}
	// Line 1 (0-based) is `    return 1 +;` — a position is only meaningful
	// once it is paired with the file it was measured in.
	if got := utilDiags[0].Range.Start.Line; got != 1 {
		t.Errorf("diagnostic on line %d, want the offending line 1", got)
	}
}

// collidingVariantWorkspace builds a workspace whose modules `a` and
// `b` each declare `Kind { Text }`. Neither imports the other, so
// neither bare `Text` is ambiguous where it is written — but the
// entry's program holds both enums at once, which is what the LSP
// resolves names against. Returns the server, the entry URI, and the
// (1-based) line/col of a.fern's `Text`.
func collidingVariantWorkspace(t *testing.T) (*Server, string, int, int) {
	t.Helper()
	dir := t.TempDir()
	aSrc := "pub enum Kind { Text }\n" +
		"pub function ka(): Kind { return Text; }\n"
	// b's use sits on its own line so the two bare `Text`s never
	// share a source position — the hit under test has to be a's.
	bSrc := "pub enum Kind { Text }\n" +
		"pub function kb(): Kind {\n" +
		"    return Text;\n" +
		"}\n"
	mainSrc := "import \"./a\";\n" +
		"import \"./b\";\n" +
		"function main(): i32 { var x: a.Kind = a.ka(); var y: b.Kind = b.kb(); return 0; }\n"
	writeFile(t, filepath.Join(dir, "a.fern"), aSrc)
	writeFile(t, filepath.Join(dir, "b.fern"), bSrc)
	mainPath := filepath.Join(dir, "main.fern")
	writeFile(t, mainPath, mainSrc)

	s := NewServer()
	s.EnableWorkspace()
	s.SetPublisher(func(string, any) {})
	mainURI := pathToURI(mainPath)
	open, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		Method:  "textDocument/didOpen",
		Params: jsonRaw(didOpenParams{TextDocument: textDocumentItem{
			URI: mainURI, LanguageID: "fern", Text: mainSrc,
		}}),
	})
	s.HandleMessage(open)
	state := s.docs[mainURI]
	if state == nil || state.prog == nil {
		t.Fatalf("workspace did not load")
	}
	if len(state.diags) != 0 {
		t.Fatalf("fixture should type-check clean, got %+v", state.diags)
	}
	line := 2
	col := strings.Index(strings.Split(aSrc, "\n")[1], "return Text") + len("return ") + 1
	return s, mainURI, line, col
}

// A variant name declared by two mutually invisible modules must
// resolve to the enum the checker stamped on the reference. The
// symbol tables are Go maps, so a name-keyed sweep picks a different
// enum from run to run — hence the repeats.
func TestWorkspace_VariantHoverNamesTheStampedEnum(t *testing.T) {
	s, mainURI, line, col := collidingVariantWorkspace(t)
	state := s.docs[mainURI]
	hit := findNameAt(state.prog, line, col)
	if hit == nil || hit.ident == nil {
		t.Fatalf("expected an Ident hit on a.fern's `Text`, got %+v", hit)
	}
	if hit.ident.EnumName != "a__Kind" {
		t.Fatalf("checker stamped %q on a's `Text`, want a__Kind", hit.ident.EnumName)
	}
	for i := 0; i < 50; i++ {
		got, ok := describeName(state.info, hit)
		if !ok {
			t.Fatalf("run %d: no description for a's `Text`", i)
		}
		if !strings.Contains(got, "a__Kind") {
			t.Fatalf("run %d: hover = %q, want a's enum", i, got)
		}
	}
	if got := classifyIdent(state.info, hit.enclosing, hit.ident); got != stEnumMember {
		t.Errorf("semantic token = %d, want stEnumMember (%d)", got, stEnumMember)
	}
}

// Find-all-references on a variant must not sweep in the like-named
// variant of an enum the referring module cannot see.
func TestWorkspace_VariantReferencesStayInTheirEnum(t *testing.T) {
	s, mainURI, line, col := collidingVariantWorkspace(t)
	state := s.docs[mainURI]
	for i := 0; i < 50; i++ {
		locs := runReferences(state, mainURI, Position{Line: line - 1, Character: col - 1})
		if len(locs) != 2 {
			t.Fatalf("run %d: %d references, want 2 (a's decl + a's use): %+v", i, len(locs), locs)
		}
		for _, l := range locs {
			if !strings.HasSuffix(l.URI, "a.fern") {
				t.Fatalf("run %d: reference in %s, want a.fern only", i, l.URI)
			}
		}
	}
}

// Go-to-definition on the same reference lands on a's declaration.
func TestWorkspace_VariantDefinitionPicksTheStampedEnum(t *testing.T) {
	s, mainURI, line, col := collidingVariantWorkspace(t)
	state := s.docs[mainURI]
	for i := 0; i < 50; i++ {
		loc := runDefinition(state, mainURI, Position{Line: line - 1, Character: col - 1})
		if loc == nil {
			t.Fatalf("run %d: no definition for a's `Text`", i)
		}
		if !strings.HasSuffix(loc.URI, "a.fern") {
			t.Fatalf("run %d: definition in %s, want a.fern", i, loc.URI)
		}
	}
}

func TestUriToPath_RejectsNonFileScheme(t *testing.T) {
	if _, ok := uriToPath("inmemory:///x.fern"); ok {
		t.Errorf("expected uriToPath to reject non-file scheme")
	}
	if _, ok := uriToPath("https://example.com/x.fern"); ok {
		t.Errorf("expected uriToPath to reject http scheme")
	}
}
