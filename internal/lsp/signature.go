package lsp

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// signatureHelpParams matches hoverParams on the wire.
type signatureHelpParams = hoverParams

// signatureHelp is the LSP response shape for textDocument/signatureHelp:
// a stack of signatures (we only ever return one) with the index of
// the currently-active parameter.
type signatureHelp struct {
	Signatures      []signatureInformation `json:"signatures"`
	ActiveSignature int                    `json:"activeSignature"`
	ActiveParameter int                    `json:"activeParameter"`
}

type signatureInformation struct {
	Label      string                 `json:"label"`
	Parameters []parameterInformation `json:"parameters,omitempty"`
}

type parameterInformation struct {
	Label string `json:"label"`
}

// runSignatureHelp inspects the source text around the cursor for the
// nearest unmatched `(` and the identifier that introduced it. If
// that identifier resolves to a known function, the response carries
// its signature with the cursor-correct active-parameter index.
// Returns nil when the cursor isn't inside a call.
//
// Operates on source text rather than the AST because the user is
// mid-typing and the surrounding expression usually doesn't parse
// cleanly. A small hand-scan tolerates that.
func runSignatureHelp(state *docState, pos Position) *signatureHelp {
	if state == nil {
		return nil
	}
	offset := lspPositionToOffset(state.src, pos)
	if offset < 0 {
		return nil
	}
	name, paramIdx, ok := findCallContext(state.src, offset)
	if !ok || state.info == nil {
		return nil
	}
	sig, ok := state.info.FuncSigs[name]
	if !ok {
		return nil
	}
	return &signatureHelp{
		Signatures: []signatureInformation{
			{
				Label:      formatFuncSig(name, sig),
				Parameters: paramInfo(sig),
			},
		},
		ActiveParameter: clampActiveParam(paramIdx, sig),
	}
}

// findCallContext scans src backward from offset looking for the most
// recent unmatched `(`. Returns (functionName, paramIndex, true) when
// the `(` is preceded by an identifier. Skips text inside string
// literals and line comments — both can hold parens that aren't part
// of a call.
//
// paramIndex is the comma count between the matching `(` and offset,
// also skipping nested parens / brackets / braces and string / comment
// content. 0 means the cursor sits on the first argument.
func findCallContext(src string, offset int) (string, int, bool) {
	if offset > len(src) {
		offset = len(src)
	}
	depth := 0
	commas := 0
	i := offset - 1
	for i >= 0 {
		c := src[i]
		switch c {
		case ')', ']', '}':
			depth++
		case '(':
			if depth == 0 {
				// Found the unmatched opener. The function name
				// ends just before this paren (after any
				// whitespace).
				name, ok := identifierEndingBefore(src, i)
				if !ok {
					return "", 0, false
				}
				return name, commas, true
			}
			depth--
		case '[', '{':
			depth--
		case ',':
			if depth == 0 {
				commas++
			}
		case '"':
			// Skip the matching opening `"` — find it by scanning
			// backward. Lang strings can contain `\\"`; the cheap
			// approximation is to find the previous unescaped `"`.
			j := i - 1
			for j >= 0 {
				if src[j] == '"' && (j == 0 || src[j-1] != '\\') {
					break
				}
				j--
			}
			if j < 0 {
				return "", 0, false
			}
			i = j
		case '\n':
			// Look backward for `//` on the same line — if there
			// is one, everything between it and `\n` is a comment
			// and we should skip it. Common case: cursor on a new
			// line after a `//` comment.
			lineStart := lastNewlineBefore(src, i) + 1
			if cmt := indexLineComment(src, lineStart, i); cmt >= 0 {
				// Reposition the scan past the comment marker —
				// the loop's i-- will land us right before `//`.
				i = cmt
			}
		}
		i--
	}
	return "", 0, false
}

// identifierEndingBefore returns the identifier text whose last char
// sits at some position < end (allowing trailing whitespace). Returns
// false when the chars before `end` aren't a valid identifier
// (digit-leading, empty, etc.).
func identifierEndingBefore(src string, end int) (string, bool) {
	i := end - 1
	for i >= 0 && (src[i] == ' ' || src[i] == '\t' || src[i] == '\n') {
		i--
	}
	if i < 0 {
		return "", false
	}
	last := i
	for i >= 0 && isIdentByte(src[i]) {
		i--
	}
	if i+1 > last {
		return "", false
	}
	first := i + 1
	if !isIdentStart(src[first]) {
		return "", false
	}
	return src[first : last+1], true
}

func isIdentStart(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isIdentByte(b byte) bool {
	return isIdentStart(b) || (b >= '0' && b <= '9')
}

func lastNewlineBefore(src string, end int) int {
	for i := end - 1; i >= 0; i-- {
		if src[i] == '\n' {
			return i
		}
	}
	return -1
}

func indexLineComment(src string, start, end int) int {
	for i := start; i+1 < end; i++ {
		if src[i] == '/' && src[i+1] == '/' {
			return i
		}
		if src[i] == '"' {
			// Skip past the string so a `//` inside doesn't fool
			// us. Same forward-scan as in findCallContext but for
			// a known forward range.
			j := i + 1
			for j < end {
				if src[j] == '"' && src[j-1] != '\\' {
					break
				}
				j++
			}
			i = j
		}
	}
	return -1
}

// lspPositionToOffset returns the byte offset into src for the given
// LSP position (0-based line + 0-based character). Returns -1 when
// the position is out of bounds. Assumes ASCII for the character
// dimension — same caveat as the toLSPPosition direction.
func lspPositionToOffset(src string, pos Position) int {
	line := 0
	col := 0
	for i := 0; i < len(src); i++ {
		if line == pos.Line && col == pos.Character {
			return i
		}
		if src[i] == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	if line == pos.Line && col == pos.Character {
		return len(src)
	}
	return -1
}

func paramInfo(sig *ast.FuncType) []parameterInformation {
	if sig == nil {
		return nil
	}
	out := make([]parameterInformation, len(sig.Params))
	for i, p := range sig.Params {
		out[i] = parameterInformation{Label: typeString(p)}
	}
	return out
}

func clampActiveParam(idx int, sig *ast.FuncType) int {
	if sig == nil || len(sig.Params) == 0 {
		return 0
	}
	if idx >= len(sig.Params) {
		return len(sig.Params) - 1
	}
	if idx < 0 {
		return 0
	}
	return idx
}

// Silence the unused-import check that triggers when the file
// stops touching checker.Info directly (paranoia for refactors).
var _ = (*checker.Info)(nil)
