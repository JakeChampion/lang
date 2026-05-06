// Package diag renders compiler errors against the original source. It
// turns a position+message pair (which lexer, parser and checker errors
// already carry) into output like:
//
//	path/foo.lang:3:9: error: undefined identifier "x"
//	    return x + 1;
//	           ^
//	    note: did you mean "y"?
//
// Errors aggregates multiple per-error diagnostics so the type checker
// can report several problems at once.
package diag

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// Positioned is satisfied by every error in this codebase that has a
// source location. Renderers use it to extract the line/column, fall
// back to the wrapped error's message via Error(), and pretty-print.
type Positioned interface {
	error
	Position() ast.Position
}

// Spanned is optionally satisfied by errors that know the length of
// the offending token. When present the underline becomes `^~~~~`
// covering the whole span instead of a single caret.
type Spanned interface {
	Positioned
	Length() int
}

// Hinted is optionally satisfied by errors that want to attach a
// follow-up note to the diagnostic — typically a "did you mean foo?"
// suggestion. The hint is rendered on its own line with a `note:`
// prefix.
type Hinted interface {
	error
	Hint() string
}

// Errors is a flat collection of errors. Useful when a pass (e.g. the
// type checker) wants to report many problems in one go.
type Errors []error

func (es Errors) Error() string {
	switch len(es) {
	case 0:
		return ""
	case 1:
		return es[0].Error()
	}
	var b strings.Builder
	for i, e := range es {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.Error())
	}
	return b.String()
}

// As lets `errors.As` traverse Errors. It picks the first match.
func (es Errors) As(target any) bool {
	for _, e := range es {
		if as, ok := e.(interface{ As(any) bool }); ok && as.As(target) {
			return true
		}
	}
	return false
}

// Format renders err with the relevant source line and a caret. If err
// is an Errors, every entry is rendered. Errors that don't satisfy
// Positioned fall back to plain Error() output. The filename is
// included in the header (Unix-tool style) when non-empty; it's
// otherwise omitted so unit tests can render without a path prefix.
func Format(filename, src string, err error) string {
	if err == nil {
		return ""
	}
	if es, ok := err.(Errors); ok {
		var b strings.Builder
		for i, e := range es {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(Format(filename, src, e))
		}
		return b.String()
	}

	pe, ok := err.(Positioned)
	if !ok {
		return err.Error()
	}
	pos := pe.Position()
	line := pickLine(src, pos.Line)
	if line == "" && pos.Line == 0 {
		return err.Error()
	}

	header := fmt.Sprintf("%d:%d: error: %s", pos.Line, pos.Col, stripPrefix(pe.Error()))
	if filename != "" {
		header = filename + ":" + header
	}

	pad := strings.Repeat(" ", clamp(pos.Col-1, 0, len(line)))
	mark := "^"
	if sp, ok := err.(Spanned); ok && sp.Length() > 1 {
		// Cap the squiggle to what fits on the line so it never wraps.
		room := len(line) - (pos.Col - 1) - 1
		if room < 0 {
			room = 0
		}
		span := sp.Length()
		if span-1 > room {
			span = room + 1
		}
		mark = "^" + strings.Repeat("~", span-1)
	}

	out := fmt.Sprintf("%s\n    %s\n    %s%s", header, line, pad, mark)
	if h, ok := err.(Hinted); ok {
		if hint := h.Hint(); hint != "" {
			out += "\n    note: " + hint
		}
	}
	return out
}

// stripPrefix removes any "<kind> error at L:C: " prefix that the
// concrete error type may have included, so Format's own header isn't
// duplicated.
func stripPrefix(msg string) string {
	if i := strings.Index(msg, ": "); i >= 0 {
		// Only strip if the prefix looks like "X error at L:C".
		head := msg[:i]
		if strings.Contains(head, " error at ") {
			return msg[i+2:]
		}
	}
	return msg
}

// pickLine returns line `n` (1-based) from src, with tabs replaced by a
// single space so the caret aligns visually.
func pickLine(src string, n int) string {
	if n <= 0 {
		return ""
	}
	cur := 1
	start := 0
	for i := 0; i < len(src); i++ {
		if cur == n {
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src)
			} else {
				end += i
			}
			return strings.ReplaceAll(src[i:end], "\t", " ")
		}
		if src[i] == '\n' {
			cur++
			start = i + 1
		}
	}
	if cur == n {
		return strings.ReplaceAll(src[start:], "\t", " ")
	}
	return ""
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Suggest returns the entry in candidates closest to name within an
// edit-distance budget that scales with name length. An empty result
// means no good suggestion was found and callers should not emit a
// hint at all.
func Suggest(name string, candidates []string) string {
	if name == "" || len(candidates) == 0 {
		return ""
	}
	budget := 2
	if len(name) <= 3 {
		budget = 1
	}
	best := ""
	bestDist := budget + 1
	for _, c := range candidates {
		if c == name {
			continue
		}
		d := levenshtein(name, c)
		if d < bestDist {
			best = c
			bestDist = d
		}
	}
	if bestDist > budget {
		return ""
	}
	return best
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
