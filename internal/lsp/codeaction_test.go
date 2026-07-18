package lsp

import (
	"encoding/json"
	"strings"
	"testing"
)

// The E001 near-miss fix flows end-to-end: updateDoc publishes a
// diagnostic whose data carries the fix; echoing that diagnostic back
// through textDocument/codeAction yields a quickfix whose edit applies
// the replacement — and applying it to the document text produces the
// corrected program.
func TestCodeActionQuickfixFromDiagnosticData(t *testing.T) {
	src := "function main(): i32 { var count = 1; return kount; }\n"
	s := NewServer()
	s.updateDoc("file:///t", src)
	state := s.docs["file:///t"]
	if state == nil || len(state.diags) == 0 {
		t.Fatal("expected a diagnostic for the misspelt identifier")
	}
	var withFix *Diagnostic
	for i := range state.diags {
		if state.diags[i].Data != nil {
			withFix = &state.diags[i]
		}
	}
	if withFix == nil {
		t.Fatalf("no diagnostic carried fix data: %+v", state.diags)
	}

	// Round-trip the diagnostic through JSON exactly as an LSP client
	// echoes it into the codeAction request context.
	wire, err := json.Marshal(withFix)
	if err != nil {
		t.Fatal(err)
	}
	req := []byte(`{"textDocument":{"uri":"file:///t"},"range":` + string(mustJSON(t, withFix.Range)) + `,"context":{"diagnostics":[` + string(wire) + `]}}`)
	res, rpcErr := (&Server{}).handleCodeAction(req)
	if rpcErr != nil {
		t.Fatalf("codeAction rpc error: %+v", rpcErr)
	}
	actions, ok := res.([]codeAction)
	if !ok || len(actions) != 1 {
		t.Fatalf("expected one code action, got %T %+v", res, res)
	}
	a := actions[0]
	if a.Kind != "quickfix" {
		t.Errorf("kind = %q, want quickfix", a.Kind)
	}
	if a.Title != "replace `kount` with `count`" {
		t.Errorf("title = %q", a.Title)
	}
	edits := a.Edit.Changes["file:///t"]
	if len(edits) != 1 {
		t.Fatalf("expected one edit, got %+v", a.Edit.Changes)
	}

	// Apply the edit (single-line span) and confirm the fix.
	e := edits[0]
	lines := strings.Split(src, "\n")
	ln := lines[e.Range.Start.Line]
	fixed := ln[:e.Range.Start.Character] + e.NewText + ln[e.Range.End.Character:]
	if !strings.Contains(fixed, "return count;") {
		t.Errorf("applied edit = %q, want the corrected identifier", fixed)
	}
}

// A diagnostic without fix data (e.g. a plain type error) yields no
// actions — the response is an empty list, not null.
func TestCodeActionNoFixData(t *testing.T) {
	req := []byte(`{"textDocument":{"uri":"file:///t"},"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"context":{"diagnostics":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"message":"type mismatch"}]}}`)
	res, rpcErr := (&Server{}).handleCodeAction(req)
	if rpcErr != nil {
		t.Fatalf("codeAction rpc error: %+v", rpcErr)
	}
	actions, ok := res.([]codeAction)
	if !ok || len(actions) != 0 {
		t.Fatalf("expected empty action list, got %T %+v", res, res)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
