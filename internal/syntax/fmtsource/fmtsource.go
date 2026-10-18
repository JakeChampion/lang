// Package fmtsource formats Fern source text: what `fern -fmt` writes for a
// .fern file or a literate .fern.md document. It sits outside
// internal/syntax/printer because the parser's tests import the printer.
package fmtsource

import (
	"strings"

	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/syntax/printer"
	"github.com/jakechampion/lang/internal/tools/literate"
)

// Format parses src and returns its formatted text.
func Format(src string) (string, error) {
	prog, err := parser.Parse(src)
	if err != nil {
		return "", err
	}
	return printer.Format(prog), nil
}

// FormatLiterate formats the code inside a literate document's chunks,
// leaving prose, fences, and chunk headers untouched; a chunk body the
// formatter cannot parse on its own stays verbatim.
func FormatLiterate(src string) string {
	return literate.Parse(src).FormatCode(formatChunkBody)
}

// formatChunkBody formats one literate chunk body. A body is formattable
// when it parses either as a complete program (top-level declarations) or
// — wrapped in a synthetic function — as a statement list. Bodies that
// parse as neither (fragments split mid-construct, or bodies containing
// `<<ref>>` chunk references, which aren't valid Fern) are declined with
// ok=false so FormatCode keeps them verbatim. Whitespace-only bodies are
// left as-is.
func formatChunkBody(code string) (string, bool) {
	if strings.TrimSpace(code) == "" {
		return "", false
	}
	if prog, err := parser.Parse(code); err == nil {
		return strings.TrimRight(printer.Format(prog), "\n"), true
	}
	if prog, err := parser.Parse(wrapOpen + code + "\n}\n"); err == nil {
		return unwrapFormattedBody(printer.Format(prog)), true
	}
	return "", false
}

// wrapOpen opens the throwaway function a statement-list body is formatted
// inside.
const wrapOpen = "function __fern_fmt_wrap__() {\n"

// unwrapFormattedBody strips the synthetic wrapper formatChunkBody added,
// returning the body de-indented by one level so it sits at the chunk's
// own column.
func unwrapFormattedBody(formatted string) string {
	lines := strings.Split(strings.TrimRight(formatted, "\n"), "\n")
	if len(lines) < 2 {
		return "" // empty wrapped block: `function …() {}`
	}
	inner := lines[1 : len(lines)-1] // drop the `function …{` and `}` lines
	for i, ln := range inner {
		inner[i] = strings.TrimPrefix(ln, formatIndentUnit)
	}
	return strings.Join(inner, "\n")
}

// formatIndentUnit is one level of the formatter's indentation, stripped
// when unwrapping a wrapped chunk body.
const formatIndentUnit = "  "
