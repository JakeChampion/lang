package lsp

import (
	"encoding/json"

	"github.com/jakechampion/lang/internal/diag"
)

// textDocument/codeAction (#4413 Rec §8, the deferred LSP half of the
// machine-applicable Suggestion foundation). The server plants each
// diag.Suggestion on its diagnostic's `data` field at publish time
// (diagnostics.go); the client round-trips those diagnostics back in
// codeAction requests' context, so the quickfix is served from the
// request alone — no server-side fix cache, and workspace mode works
// for free.

// fixData is the data payload planted on a Diagnostic that carries a
// machine-applicable fix. It is already in LSP coordinates so the
// codeAction handler can lift it into a TextEdit verbatim.
type fixData struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
	Title   string `json:"title"`
}

// rangeOfFix converts a diag.Suggestion's 1-based byte span to the
// LSP range it replaces.
func rangeOfFix(fix *diag.Suggestion) Range {
	start := toLSPPosition(fix.Pos)
	end := start
	end.Character = start.Character + fix.Length
	return Range{Start: start, End: end}
}

// codeActionParams is the request subset we consume. Diagnostics'
// data arrives as raw JSON (the client echoes whatever we published),
// so the paramDiagnostic shape keeps it raw for a typed re-decode.
type codeActionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Range   Range `json:"range"`
	Context struct {
		Diagnostics []paramDiagnostic `json:"diagnostics"`
	} `json:"context"`
}

type paramDiagnostic struct {
	Range   Range           `json:"range"`
	Code    string          `json:"code,omitempty"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// codeAction is the response entry: a quickfix applying the
// suggestion's replacement over its span.
type codeAction struct {
	Title       string            `json:"title"`
	Kind        string            `json:"kind"`
	Diagnostics []paramDiagnostic `json:"diagnostics,omitempty"`
	Edit        *workspaceEdit    `json:"edit,omitempty"`
}

// runCodeAction builds one quickfix per context diagnostic whose data
// decodes as a fixData. Returns an empty slice when none do, so the
// JSON encoder emits `[]` per LSP convention.
func runCodeAction(uri string, p codeActionParams) []codeAction {
	out := []codeAction{}
	for _, d := range p.Context.Diagnostics {
		if len(d.Data) == 0 {
			continue
		}
		var fix fixData
		if err := json.Unmarshal(d.Data, &fix); err != nil || fix.NewText == "" || fix.Title == "" {
			continue
		}
		out = append(out, codeAction{
			Title:       fix.Title,
			Kind:        "quickfix",
			Diagnostics: []paramDiagnostic{d},
			Edit: &workspaceEdit{
				Changes: map[string][]textEdit{
					uri: {{Range: fix.Range, NewText: fix.NewText}},
				},
			},
		})
	}
	return out
}
