package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

// diagnosticsFor is a test helper that runs src through the same
// pipeline updateDoc does and returns just the diagnostics — saves
// callers from constructing a Server and pulling state out of its
// docs map.
func diagnosticsFor(src string) []Diagnostic {
	s := NewServer()
	s.updateDoc("file:///t", src)
	return s.docs["file:///t"].diags
}

// ---- diagnostics translation ----

func TestRunDiagnostics_CleanSource(t *testing.T) {
	src := `function main(): i32 { return 0; }`
	got := diagnosticsFor(src)
	if len(got) != 0 {
		t.Fatalf("clean source produced %d diagnostics: %+v", len(got), got)
	}
}

func TestRunDiagnostics_ParserError(t *testing.T) {
	// Missing closing brace.
	src := `function main(): i32 { return 0;`
	got := diagnosticsFor(src)
	if len(got) == 0 {
		t.Fatal("expected at least one diagnostic for unterminated function")
	}
	for _, d := range got {
		if d.Severity != severityError {
			t.Errorf("diagnostic severity = %d, want %d", d.Severity, severityError)
		}
		if d.Source != "lang" {
			t.Errorf("diagnostic source = %q, want %q", d.Source, "lang")
		}
		if d.Message == "" {
			t.Errorf("diagnostic message is empty")
		}
		if strings.Contains(d.Message, " error at ") {
			t.Errorf("diagnostic message should have position prefix stripped, got %q", d.Message)
		}
	}
}

func TestRunDiagnostics_TypeError(t *testing.T) {
	// Unknown identifier — parses cleanly, fails in checker.
	src := `function main(): i32 { return undeclared_name; }`
	got := diagnosticsFor(src)
	if len(got) == 0 {
		t.Fatal("expected at least one diagnostic for unknown identifier")
	}
	// The checker emits a Spanned + Hinted error here, so we should
	// see the hint inline and a multi-char range.
	d := got[0]
	if d.Range.End.Character <= d.Range.Start.Character {
		t.Errorf("expected non-empty range, got %+v", d.Range)
	}
	// Position prefix should be stripped even on Hinted checker
	// errors. Regression guard for the bug where Hinted+empty-hint
	// errors leaked the "type error at L:C: " prefix.
	if strings.Contains(d.Message, " error at ") {
		t.Errorf("position prefix should be stripped, got %q", d.Message)
	}
}

// `undefined identifier` is stamped E001 in the checker
// (docs/DIAGNOSTIC-UX-RESEARCH.md Rec §4). The LSP frame
// must carry that code in the `code` field so IDEs can
// deep-link to `lang explain E001`.
func TestRunDiagnostics_ErrorCodePopulated(t *testing.T) {
	src := `function main(): i32 { return undeclared_name; }`
	got := diagnosticsFor(src)
	if len(got) == 0 {
		t.Fatal("expected at least one diagnostic")
	}
	if got[0].Code != "E001" {
		t.Errorf("diagnostic.Code = %q, want %q", got[0].Code, "E001")
	}
}

// Diagnostics from errors without a stable code keep the
// field empty — the `omitempty` json tag drops it from the
// wire payload. Regression sentinel against the wrapper
// accidentally stamping a synthetic code on every error.
//
// As the catalogue grows, fewer error shapes remain
// un-coded. This fixture uses an unterminated string
// literal — the lexer-level error currently has no code
// (lexer errors aren't yet plumbed through the catalogue).
func TestRunDiagnostics_NoCodeWhenNoneAssigned(t *testing.T) {
	src := `function main(): i32 { return "unterminated;`
	got := diagnosticsFor(src)
	if len(got) == 0 {
		t.Fatal("expected at least one diagnostic")
	}
	if got[0].Code != "" {
		t.Errorf("diagnostic.Code = %q, want empty (no code assigned)", got[0].Code)
	}
}

func TestRunDiagnostics_PositionsAreZeroBased(t *testing.T) {
	// Force a checker error on line 3.
	src := "function main(): i32 {\n  var x: i32 = 1;\n  return undeclared;\n}\n"
	got := diagnosticsFor(src)
	if len(got) == 0 {
		t.Fatal("expected diagnostic")
	}
	// Find the diagnostic that talks about the undeclared name.
	var d *Diagnostic
	for i := range got {
		if strings.Contains(got[i].Message, "undeclared") {
			d = &got[i]
			break
		}
	}
	if d == nil {
		t.Fatalf("no diagnostic mentioned undeclared: %+v", got)
	}
	// Source line 3 → LSP line 2 (0-based). The exact column varies
	// by token-end placement; we just sanity-check the line.
	if d.Range.Start.Line != 2 {
		t.Errorf("diagnostic start line = %d, want 2 (0-based)", d.Range.Start.Line)
	}
}

// ---- HandleMessage (wasm-style driver) ----

func TestHandleMessage_InitializeReturnsCapabilities(t *testing.T) {
	s := NewServer()
	req := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	resp := s.HandleMessage([]byte(req))
	if len(resp) == 0 {
		t.Fatal("initialize returned no response")
	}
	var m message
	if err := json.Unmarshal(resp, &m); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if m.Error != nil {
		t.Fatalf("initialize returned error: %+v", m.Error)
	}
	var got initializeResult
	if err := json.Unmarshal(m.Result, &got); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}
	if got.Capabilities.TextDocumentSync != syncKindFull {
		t.Errorf("textDocumentSync = %d, want %d", got.Capabilities.TextDocumentSync, syncKindFull)
	}
	if got.ServerInfo == nil || got.ServerInfo.Name != "lang-lsp" {
		t.Errorf("serverInfo = %+v", got.ServerInfo)
	}
}

func TestHandleMessage_NotificationProducesNoResponse(t *testing.T) {
	s := NewServer()
	// `initialized` is a notification (no id).
	resp := s.HandleMessage([]byte(`{"jsonrpc":"2.0","method":"initialized","params":{}}`))
	if resp != nil {
		t.Errorf("notification produced response: %s", resp)
	}
}

func TestHandleMessage_UnknownMethodReturnsError(t *testing.T) {
	s := NewServer()
	resp := s.HandleMessage([]byte(`{"jsonrpc":"2.0","id":7,"method":"textDocument/wat","params":{}}`))
	var m message
	if err := json.Unmarshal(resp, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Error == nil {
		t.Fatalf("expected error, got result: %s", m.Result)
	}
	if m.Error.Code != errCodeMethodNotFound {
		t.Errorf("error code = %d, want %d", m.Error.Code, errCodeMethodNotFound)
	}
}

func TestHandleMessage_BadJSONReturnsParseError(t *testing.T) {
	s := NewServer()
	resp := s.HandleMessage([]byte(`{not json`))
	var m message
	if err := json.Unmarshal(resp, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Error == nil || m.Error.Code != errCodeParseError {
		t.Errorf("expected parse error, got %+v", m.Error)
	}
}

func TestHandleMessage_DidOpenPublishesDiagnostics(t *testing.T) {
	s := NewServer()
	var published []publishDiagnosticsParams
	s.SetPublisher(func(method string, params any) {
		if method != "textDocument/publishDiagnostics" {
			return
		}
		// Round-trip through JSON so the test sees the same shape
		// the wire would carry.
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal publish params: %v", err)
		}
		var p publishDiagnosticsParams
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatalf("unmarshal publish params: %v", err)
		}
		published = append(published, p)
	})

	// didOpen with a parse error — expect at least one diagnostic.
	openParams := didOpenParams{
		TextDocument: textDocumentItem{
			URI:        "file:///tmp/main.lang",
			LanguageID: "lang",
			Version:    1,
			Text:       "function main(): i32 { return 0;",
		},
	}
	rawParams, _ := json.Marshal(openParams)
	notif := message{
		Jsonrpc: "2.0",
		Method:  "textDocument/didOpen",
		Params:  rawParams,
	}
	body, _ := json.Marshal(notif)
	if resp := s.HandleMessage(body); resp != nil {
		t.Fatalf("didOpen notification produced response: %s", resp)
	}
	if len(published) != 1 {
		t.Fatalf("expected 1 publishDiagnostics, got %d", len(published))
	}
	if published[0].URI != openParams.TextDocument.URI {
		t.Errorf("publish uri = %q, want %q", published[0].URI, openParams.TextDocument.URI)
	}
	if len(published[0].Diagnostics) == 0 {
		t.Errorf("expected diagnostics for unterminated function, got none")
	}
}

func TestHandleMessage_DidChangeRepublishes(t *testing.T) {
	// Mirrors the playground's edit loop: open with an error,
	// expect a non-empty publish; change to clean source, expect
	// a follow-up empty publish. The playground relies on the
	// "cleared diagnostics" notification to turn off its red
	// problem-count badge after the user fixes a typo.
	s := NewServer()
	var publishes []publishDiagnosticsParams
	s.SetPublisher(func(method string, params any) {
		if method != "textDocument/publishDiagnostics" {
			return
		}
		b, _ := json.Marshal(params)
		var p publishDiagnosticsParams
		_ = json.Unmarshal(b, &p)
		publishes = append(publishes, p)
	})

	openMsg, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		Method:  "textDocument/didOpen",
		Params: jsonRaw(didOpenParams{
			TextDocument: textDocumentItem{
				URI:        "file:///live.lang",
				LanguageID: "lang",
				Version:    1,
				Text:       "function main(): i32 { return undeclared; }",
			},
		}),
	})
	s.HandleMessage(openMsg)

	var ch didChangeParams
	ch.TextDocument.URI = "file:///live.lang"
	ch.TextDocument.Version = 2
	ch.ContentChanges = []contentChange{{Text: "function main(): i32 { return 0; }"}}
	changeMsg, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		Method:  "textDocument/didChange",
		Params:  jsonRaw(ch),
	})
	s.HandleMessage(changeMsg)

	if len(publishes) != 2 {
		t.Fatalf("expected 2 publishDiagnostics frames, got %d", len(publishes))
	}
	if len(publishes[0].Diagnostics) == 0 {
		t.Errorf("first publish should carry errors, got none")
	}
	if len(publishes[1].Diagnostics) != 0 {
		t.Errorf("second publish should be empty (errors fixed), got %d", len(publishes[1].Diagnostics))
	}
}

func TestHandleMessage_DidCloseClearsDiagnostics(t *testing.T) {
	s := NewServer()
	var lastClearURI string
	var lastClearCount int
	s.SetPublisher(func(method string, params any) {
		if method != "textDocument/publishDiagnostics" {
			return
		}
		b, _ := json.Marshal(params)
		var p publishDiagnosticsParams
		_ = json.Unmarshal(b, &p)
		lastClearURI = p.URI
		lastClearCount = len(p.Diagnostics)
	})
	// Open then close.
	openMsg, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		Method:  "textDocument/didOpen",
		Params: jsonRaw(didOpenParams{TextDocument: textDocumentItem{
			URI:  "file:///tmp/x.lang",
			Text: "function main(): i32 { return 0; }",
		}}),
	})
	s.HandleMessage(openMsg)

	closeParams := didCloseParams{}
	closeParams.TextDocument.URI = "file:///tmp/x.lang"
	closeMsg, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		Method:  "textDocument/didClose",
		Params:  jsonRaw(closeParams),
	})
	s.HandleMessage(closeMsg)

	if lastClearURI != "file:///tmp/x.lang" {
		t.Errorf("close didn't publish for the closed uri, got %q", lastClearURI)
	}
	if lastClearCount != 0 {
		t.Errorf("close should publish empty diagnostics, got %d", lastClearCount)
	}
}

// ---- Serve (stdio) end-to-end ----

func TestServe_InitializeRoundTrip(t *testing.T) {
	in := newFrameBuf()
	in.write(t, message{
		Jsonrpc: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "initialize",
		Params:  json.RawMessage(`{}`),
	})
	// Force EOF after the single message so Serve returns.
	var out bytes.Buffer
	s := NewServer()
	if err := s.Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	frames := readAllFrames(t, &out)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d: %v", len(frames), frames)
	}
	var m message
	if err := json.Unmarshal(frames[0], &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(m.ID) != "1" {
		t.Errorf("response id = %s, want 1", m.ID)
	}
	if m.Error != nil {
		t.Errorf("initialize errored: %+v", m.Error)
	}
}

func TestServe_DidOpenThenShutdownThenExit(t *testing.T) {
	in := newFrameBuf()
	in.write(t, message{Jsonrpc: "2.0", ID: json.RawMessage("1"), Method: "initialize", Params: json.RawMessage(`{}`)})
	in.write(t, message{Jsonrpc: "2.0", Method: "initialized", Params: json.RawMessage(`{}`)})
	in.write(t, message{
		Jsonrpc: "2.0",
		Method:  "textDocument/didOpen",
		Params: jsonRaw(didOpenParams{TextDocument: textDocumentItem{
			URI:  "file:///tmp/bad.lang",
			Text: "function main(): i32 { return undeclared; }",
		}}),
	})
	in.write(t, message{Jsonrpc: "2.0", ID: json.RawMessage("2"), Method: "shutdown"})
	in.write(t, message{Jsonrpc: "2.0", Method: "exit"})

	var out bytes.Buffer
	s := NewServer()
	if err := s.Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if s.ExitCode() != 0 {
		t.Errorf("exit code = %d, want 0 (shutdown then exit)", s.ExitCode())
	}

	frames := readAllFrames(t, &out)
	// Expect: initialize response, publishDiagnostics for didOpen,
	// shutdown response. (initialized and exit are notifications.)
	if len(frames) != 3 {
		t.Fatalf("expected 3 frames, got %d", len(frames))
	}

	var seenInitResp, seenShutdownResp, seenDiagnostics bool
	for _, f := range frames {
		var m message
		if err := json.Unmarshal(f, &m); err != nil {
			t.Fatalf("unmarshal frame: %v", err)
		}
		switch {
		case string(m.ID) == "1":
			seenInitResp = true
		case string(m.ID) == "2":
			seenShutdownResp = true
		case m.Method == "textDocument/publishDiagnostics":
			seenDiagnostics = true
			var p publishDiagnosticsParams
			if err := json.Unmarshal(m.Params, &p); err != nil {
				t.Fatalf("unmarshal publish params: %v", err)
			}
			if p.URI != "file:///tmp/bad.lang" {
				t.Errorf("diagnostics uri = %q, want bad.lang", p.URI)
			}
			if len(p.Diagnostics) == 0 {
				t.Errorf("expected diagnostics, got none")
			}
		}
	}
	if !seenInitResp {
		t.Errorf("missing initialize response")
	}
	if !seenShutdownResp {
		t.Errorf("missing shutdown response")
	}
	if !seenDiagnostics {
		t.Errorf("missing publishDiagnostics notification")
	}
}

func TestServe_ExitWithoutShutdownIsCodeOne(t *testing.T) {
	in := newFrameBuf()
	in.write(t, message{Jsonrpc: "2.0", Method: "exit"})
	var out bytes.Buffer
	s := NewServer()
	if err := s.Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if s.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1 (exit without shutdown)", s.ExitCode())
	}
}

// ---- helpers ----

type frameBuf struct{ buf bytes.Buffer }

func newFrameBuf() *frameBuf { return &frameBuf{} }

func (f *frameBuf) Read(p []byte) (int, error) { return f.buf.Read(p) }

func (f *frameBuf) write(t *testing.T, m message) {
	t.Helper()
	if err := writeFrame(&f.buf, m); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
}

func readAllFrames(t *testing.T, r io.Reader) [][]byte {
	t.Helper()
	br := bufio.NewReader(r)
	var out [][]byte
	for {
		frame, err := readFrame(br)
		if err == io.EOF {
			return out
		}
		if err != nil {
			// EOF can surface as "missing Content-Length header" when
			// the stream ran dry on a header line — treat as done.
			if err.Error() == "missing Content-Length header" {
				return out
			}
			t.Fatalf("readFrame: %v", err)
		}
		out = append(out, frame)
	}
}

func jsonRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("jsonRaw: %v", err))
	}
	return b
}
