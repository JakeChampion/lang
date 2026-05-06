// Package diag renders compiler errors against the original source. It
// turns a position+message pair (which lexer, parser and checker errors
// already carry) into output like:
//
//	error at 3:9: undefined identifier "x"
//	    return x + 1;
//	           ^
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
// Positioned fall back to plain Error() output.
func Format(src string, err error) string {
	if err == nil {
		return ""
	}
	if es, ok := err.(Errors); ok {
		var b strings.Builder
		for i, e := range es {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(Format(src, e))
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
	return fmt.Sprintf("error at %d:%d: %s\n    %s\n    %s^",
		pos.Line, pos.Col, stripPrefix(pe.Error()),
		line,
		strings.Repeat(" ", clamp(pos.Col-1, 0, len(line))),
	)
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
