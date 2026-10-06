package lsp

import (
	"encoding/json"
	"strings"
	"testing"
)

func signatureFor(src string, line, col int) *signatureHelp {
	s := NewServer()
	s.updateDoc("file:///t", src)
	return runSignatureHelp(s.docs["file:///t"], Position{Line: line, Character: col})
}

func TestSignatureHelp_FirstArg(t *testing.T) {
	// `add(1, 2)` — cursor right after `add(`, on the `1`.
	src := "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n"
	got := signatureFor(src, 1, 34) // 0-based line 1, after `add(`
	if got == nil {
		t.Fatal("expected signature help inside add(...)")
	}
	if len(got.Signatures) != 1 {
		t.Fatalf("expected 1 signature, got %d", len(got.Signatures))
	}
	if !strings.Contains(got.Signatures[0].Label, "add") {
		t.Errorf("signature label = %q, want it to mention add", got.Signatures[0].Label)
	}
	if got.ActiveParameter != 0 {
		t.Errorf("active parameter = %d, want 0", got.ActiveParameter)
	}
}

func TestSignatureHelp_SecondArg(t *testing.T) {
	src := "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n"
	// Cursor after `add(1, ` (0-based col 37).
	got := signatureFor(src, 1, 37)
	if got == nil {
		t.Fatal("expected signature help")
	}
	if got.ActiveParameter != 1 {
		t.Errorf("active parameter = %d, want 1", got.ActiveParameter)
	}
}

func TestSignatureHelp_NestedCallTracksInner(t *testing.T) {
	// `add(add(1, 2), 3)` — cursor on `1`, signature should be the
	// inner call (also `add`), active param 0.
	src := "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(add(1, 2), 3); }\n"
	got := signatureFor(src, 1, 38) // after inner `add(`
	if got == nil {
		t.Fatal("expected signature help in nested call")
	}
	if got.ActiveParameter != 0 {
		t.Errorf("active parameter = %d, want 0", got.ActiveParameter)
	}
}

func TestSignatureHelp_OutsideAnyCall(t *testing.T) {
	src := "function main(): i32 { return 0; }\n"
	got := signatureFor(src, 0, 20) // on the `return`
	if got != nil {
		t.Errorf("expected nil signature help outside any call, got %+v", got)
	}
}

func TestSignatureHelp_UnknownFunction(t *testing.T) {
	src := "function main(): i32 { return undeclared_fn(1, 2); }\n"
	got := signatureFor(src, 0, 45)
	if got != nil {
		t.Errorf("expected nil for unknown function, got %+v", got)
	}
}

func TestSignatureHelp_StringWithCommaIgnored(t *testing.T) {
	// The string "a, b" shouldn't count as a real argument-separator
	// comma. Cursor on the second arg (after the literal).
	src := "function fmt(a: string, b: i32): i32 { return b; }\nfunction main(): i32 { return fmt(\"a, b, c\", 5); }\n"
	got := signatureFor(src, 1, 45) // after `, ` past the string
	if got == nil {
		t.Fatal("expected signature help")
	}
	if got.ActiveParameter != 1 {
		t.Errorf("active parameter = %d, want 1 (comma inside string must not count)", got.ActiveParameter)
	}
}

func TestSignatureHelp_LinkedToFuncSig(t *testing.T) {
	src := "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n"
	got := signatureFor(src, 1, 34)
	if got == nil {
		t.Fatal("expected signature help")
	}
	want := "function add(i32, i32): i32"
	if got.Signatures[0].Label != want {
		t.Errorf("label = %q, want %q", got.Signatures[0].Label, want)
	}
	if len(got.Signatures[0].Parameters) != 2 {
		t.Errorf("expected 2 parameters, got %d", len(got.Signatures[0].Parameters))
	}
}

func TestHandleMessage_SignatureHelp(t *testing.T) {
	s := NewServer()
	s.SetPublisher(func(string, any) {})
	open := jsonRaw(didOpenParams{
		TextDocument: textDocumentItem{
			URI:        "file:///s.fern",
			LanguageID: "fern",
			Text:       "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n",
		},
	})
	openMsg, _ := json.Marshal(message{Jsonrpc: "2.0", Method: "textDocument/didOpen", Params: open})
	s.HandleMessage(openMsg)

	sp := signatureHelpParams{}
	sp.TextDocument.URI = "file:///s.fern"
	sp.Position = Position{Line: 1, Character: 37}
	msg, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		ID:      json.RawMessage("21"),
		Method:  "textDocument/signatureHelp",
		Params:  jsonRaw(sp),
	})
	resp := s.HandleMessage(msg)
	var m message
	if err := json.Unmarshal(resp, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Error != nil {
		t.Fatalf("signatureHelp errored: %+v", m.Error)
	}
	var sh signatureHelp
	if err := json.Unmarshal(m.Result, &sh); err != nil {
		t.Fatalf("unmarshal sig help: %v", err)
	}
	if len(sh.Signatures) != 1 {
		t.Errorf("expected 1 signature, got %d", len(sh.Signatures))
	}
	if sh.ActiveParameter != 1 {
		t.Errorf("active = %d, want 1", sh.ActiveParameter)
	}
}
