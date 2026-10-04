package lsp

import "github.com/jakechampion/lang/internal/fmtsource"

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
// document with what `fern -fmt` writes for it. Returns nil when the
// document doesn't parse cleanly — applying a partial format to
// broken source would silently delete the bits we couldn't
// reconstruct, which is worse than asking the user to fix the
// syntax first.
//
// It formats the document's own text, never state.prog: in workspace
// mode that is the loaded program, builtin declarations and all.
func runFormatting(state *docState) []textEdit {
	if state == nil || state.lit != nil {
		return nil
	}
	formatted, err := fmtsource.Format(state.src)
	if err != nil {
		return nil
	}
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
			Line: line,
			// UTF-16 units, not bytes — the client replaces this range, and
			// the last line may hold non-ASCII (#8468).
			Character: utf16Len(src[lastLineStart:]),
		},
	}
}
