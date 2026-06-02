package literate

import (
	"strings"
	"testing"
)

// FormatCode rewrites fern chunk bodies via the supplied formatter while
// leaving prose, fences, and headers untouched; bodies the formatter
// declines are preserved verbatim.
func TestFormatCode(t *testing.T) {
	src := strings.Join([]string{
		"# Title",
		"",
		"prose stays put",
		"",
		"```fern",
		"<<greet>>=",
		"messy body",
		"```",
		"",
		"```fern",
		"<<keep>>=",
		"leave me",
		"```",
	}, "\n")

	// Formatter that upper-cases a body, but declines the `keep` chunk.
	format := func(code string) (string, bool) {
		if strings.Contains(code, "leave me") {
			return "", false
		}
		return strings.ToUpper(code), true
	}

	got := Parse(src).FormatCode(format)

	if !strings.Contains(got, "# Title") || !strings.Contains(got, "prose stays put") {
		t.Errorf("prose should be preserved:\n%s", got)
	}
	if !strings.Contains(got, "<<greet>>=\nMESSY BODY") {
		t.Errorf("greet body should be reformatted (header preserved):\n%s", got)
	}
	if !strings.Contains(got, "<<keep>>=\nleave me") {
		t.Errorf("declined body should be verbatim:\n%s", got)
	}
	// Fences are intact (four ```fern openers/closers → 2 of each).
	if n := strings.Count(got, "```"); n != 4 {
		t.Errorf("expected 4 fence delimiters, got %d:\n%s", n, got)
	}
}

// A document with no formattable change round-trips byte-for-byte when
// the formatter is the identity-decliner.
func TestFormatCodePreservesUnchanged(t *testing.T) {
	src := "intro\n\n```fern\n<<a>>=\nx\n```\n\noutro\n"
	got := Parse(src).FormatCode(func(string) (string, bool) { return "", false })
	if got != src {
		t.Errorf("declining everything should reproduce the source exactly:\ngot:  %q\nwant: %q", got, src)
	}
}

// File-root bodies (no `<<name>>=` header) are reformatted too.
func TestFormatCodeFileRoot(t *testing.T) {
	src := "```fern file=m.fern\nbody here\n```\n"
	got := Parse(src).FormatCode(func(code string) (string, bool) {
		return strings.ToUpper(strings.TrimSpace(code)), true
	})
	if !strings.Contains(got, "file=m.fern\nBODY HERE") {
		t.Errorf("file-root body should reformat under its directive fence:\n%s", got)
	}
}
