package lsp

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/diag"
)

// toDiagnostics flattens a diag.Errors (or a single error) into LSP
// Diagnostic structs. Anything that doesn't carry source position
// info is reported at the start of the file with the raw message.
func toDiagnostics(err error) []Diagnostic {
	if err == nil {
		return nil
	}
	if es, ok := err.(diag.Errors); ok {
		out := make([]Diagnostic, 0, len(es))
		for _, e := range es {
			out = append(out, toDiagnostic(e))
		}
		return out
	}
	return []Diagnostic{toDiagnostic(err)}
}

func toDiagnostic(err error) Diagnostic {
	d := Diagnostic{
		Severity: severityError,
		Source:   "fern",
		Message:  err.Error(),
	}
	if p, ok := err.(diag.Positioned); ok {
		pos := p.Position()
		start := toLSPPosition(pos)
		// Span comes from the Spanned interface when the error
		// knows the offending token's length; otherwise underline
		// a single character (the LSP-recommended fallback for
		// "I don't know how wide this is").
		end := start
		span := 1
		if s, ok := err.(diag.Spanned); ok && s.Length() > 0 {
			span = s.Length()
		}
		end.Character = start.Character + span
		d.Range = Range{Start: start, End: end}
		// The Range already conveys position; strip the redundant
		// "<kind> error at L:C: " prefix from the message body.
		// Hinted suggestions ride inline so editors that don't
		// render the related-information block still see them —
		// saves a second protocol round-trip in the MVP.
		d.Message = stripPositionPrefix(err.Error())
		if h, ok := err.(diag.Hinted); ok {
			if hint := h.Hint(); hint != "" {
				d.Message += " (hint: " + hint + ")"
			}
		}
	}
	// Stable error code per docs/DIAGNOSTIC-UX-RESEARCH.md
	// Rec §4. Surfaced as `diagnostic.code` in the LSP frame
	// so IDEs can deep-link to the catalogue (`lang explain
	// CODE`). Empty Code() falls through with no field — the
	// `omitempty` JSON tag drops it from the wire payload.
	if c, ok := err.(diag.Coded); ok {
		d.Code = c.Code()
	}
	// A machine-applicable fix (diag.Suggestion, Rec §3) rides the
	// diagnostic's data field; textDocument/codeAction turns it into
	// a quickfix WorkspaceEdit (codeaction.go).
	if sg, ok := err.(diag.Suggested); ok {
		if fix := sg.Suggestion(); fix != nil {
			d.Data = fixData{
				Range:   rangeOfFix(fix),
				NewText: fix.Replacement,
				Title:   fix.Title,
			}
		}
	}
	return d
}

// toLSPPosition converts lang's 1-based UTF-8 byte position to LSP's
// 0-based UTF-16 character position. Exact for ASCII; off for non-
// ASCII identifiers / string contents — flagged for a follow-up.
func toLSPPosition(p ast.Position) Position {
	line := p.Line - 1
	if line < 0 {
		line = 0
	}
	col := p.Col - 1
	if col < 0 {
		col = 0
	}
	return Position{Line: line, Character: col}
}

// stripPositionPrefix removes the "parse error at L:C: " /
// "type error at L:C: " prefix the concrete error types prepend
// in their Error() string. The Range already conveys position;
// duplicating it in the message body is noise in LSP clients.
func stripPositionPrefix(msg string) string {
	for i := 0; i+1 < len(msg); i++ {
		if msg[i] == ':' && msg[i+1] == ' ' {
			head := msg[:i]
			if containsErrorAt(head) {
				return msg[i+2:]
			}
		}
	}
	return msg
}

func containsErrorAt(s string) bool {
	// Cheap substring scan — `strings.Contains` would do, but the
	// helper avoids an import for one call site.
	const needle = " error at "
	if len(s) < len(needle) {
		return false
	}
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
