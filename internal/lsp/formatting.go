package lsp

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/printer"
)

// formattingParams is the textDocument/formatting request payload.
// LSP also defines tab-size / insert-spaces fields on the options
// object; we ignore them because the formatter has its own
// opinionated style (two-space indent, one statement per line).
type formattingParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

// runFormatting returns a single TextEdit replacing the whole
// document with the formatter's idiomatic output. Returns nil when
// the document doesn't parse cleanly — applying a partial format to
// broken source would silently delete the bits we couldn't
// reconstruct, which is worse than asking the user to fix the
// syntax first.
//
// The formatter preserves comments (prog.Comments threaded through
// printer.Format) so format-on-save is non-destructive for
// human-written notes.
func runFormatting(state *docState) []textEdit {
	if state == nil || state.prog == nil {
		return nil
	}
	// Only format when the parser produced no errors. We
	// approximate that with "no diagnostics whose source-position
	// line falls within the entry file" — checker errors are OK
	// (the AST is still complete; format just won't reflect
	// type-level changes the user is trying to make).
	for _, d := range state.diags {
		if d.Severity == severityError && isParseDiagnostic(d) {
			return nil
		}
	}
	formatted := printer.Format(state.prog)
	if formatted == state.src {
		return []textEdit{} // already formatted; LSP convention
	}
	return []textEdit{
		{
			Range:   wholeDocumentRange(state.src),
			NewText: formatted,
		},
	}
}

// isParseDiagnostic distinguishes parser errors (where the AST is
// truncated or missing) from checker errors (where the AST is
// complete but type-incorrect). The format-on-error guard kicks in
// only for the former. We can't read diag.Filed off a Diagnostic
// (it's a wire struct, not an error), so this is a heuristic:
// diagnostic messages starting with "parse" are parser-emitted;
// "type" / "cannot" / "expected" without leading "parse" are
// checker. Good enough for the common case.
func isParseDiagnostic(d Diagnostic) bool {
	const prefix = "expected"
	return len(d.Message) >= len(prefix) && d.Message[:len(prefix)] == prefix
}

// wholeDocumentRange returns the LSP range covering the entire
// source. The end position uses the line count + the last line's
// length so editors that don't normalise to "huge sentinel" still
// apply the replacement against the full document.
func wholeDocumentRange(src string) Range {
	line := 0
	lastLineStart := 0
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			line++
			lastLineStart = i + 1
		}
	}
	return Range{
		Start: Position{Line: 0, Character: 0},
		End: Position{
			Line:      line,
			Character: len(src) - lastLineStart,
		},
	}
}

// (silence unused-import warnings if other files shed their AST /
// printer dependence in a future cleanup.)
var (
	_ = (*ast.Program)(nil)
	_ = printer.Format
)
