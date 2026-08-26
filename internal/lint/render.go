package lint

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/diag"
)

// ANSI SGR sequences, matching internal/diag's palette so a lint sits next
// to a compiler diagnostic without looking like it came from another tool.
// Yellow is the one addition: severity colour is the whole point of a
// warning, and diag has no non-fatal level to borrow from.
const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[1;31m"
	ansiYellow = "\x1b[1;33m"
	ansiBlue   = "\x1b[34m"
)

func paint(sgr, s string) string {
	if !diag.ColorEnabled() {
		return s
	}
	return sgr + s + ansiReset
}

// Render formats one finding in the compiler's diagnostic layout — header,
// source line, caret, help — with the rule name in the header where an
// error code would sit, since the rule name IS what a reader looks up or
// silences. srcOf supplies a file's text; a nil result or a missing line
// degrades to the header alone rather than inventing context.
func Render(f Finding, srcOf func(file string) string) string {
	label, colour := "warning", ansiYellow
	if f.Severity == Deny {
		label, colour = "error", ansiRed
	}
	header := fmt.Sprintf("%d:%d: %s: %s", f.Pos.Line, f.Pos.Col, paint(colour, label+"["+f.Rule+"]"), f.Msg)
	if f.File != "" {
		header = f.File + ":" + header
	}

	var src string
	if srcOf != nil {
		src = srcOf(f.File)
	}
	line := pickLine(src, f.Pos.Line)
	if line == "" {
		if f.Help != "" {
			header += "\n    " + paint(ansiBlue, "help:") + " " + f.Help
		}
		return header
	}

	pad := strings.Repeat(" ", clamp(f.Pos.Col-1, 0, len(line)))
	out := fmt.Sprintf("%s\n    %s\n    %s%s", header, line, pad, paint(colour, "^"))
	if f.Help != "" {
		out += "\n    " + paint(ansiBlue, "help:") + " " + f.Help
	}
	return out
}

// RenderAll formats every finding, one per paragraph, in the order given.
func RenderAll(fs []Finding, srcOf func(file string) string) string {
	var b strings.Builder
	for i, f := range fs {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(Render(f, srcOf))
		b.WriteByte('\n')
	}
	return b.String()
}

// Summary is the closing count line — the part a CI log is read for.
// Returns "" when there is nothing to say.
func Summary(fs []Finding) string {
	warns, denies := 0, 0
	for _, f := range fs {
		switch f.Severity {
		case Deny:
			denies++
		case Warn:
			warns++
		}
	}
	var parts []string
	if denies > 0 {
		parts = append(parts, plural(denies, "error"))
	}
	if warns > 0 {
		parts = append(parts, plural(warns, "warning"))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " and ") + " generated"
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// pickLine returns the 1-based nth line of src with trailing CR stripped,
// or "" when src has no such line.
func pickLine(src string, n int) string {
	if n < 1 {
		return ""
	}
	lines := strings.Split(src, "\n")
	if n > len(lines) {
		return ""
	}
	return strings.TrimSuffix(lines[n-1], "\r")
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
