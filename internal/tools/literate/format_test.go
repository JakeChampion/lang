package literate

import (
	"strings"
	"testing"
)

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
	if n := strings.Count(got, "```"); n != 4 {
		t.Errorf("expected 4 fence delimiters, got %d:\n%s", n, got)
	}
}

func TestFormatCodePreservesUnchanged(t *testing.T) {
	src := "intro\n\n```fern\n<<a>>=\nx\n```\n\noutro\n"
	got := Parse(src).FormatCode(func(string) (string, bool) { return "", false })
	if got != src {
		t.Errorf("declining everything should reproduce the source exactly:\ngot:  %q\nwant: %q", got, src)
	}
}

func TestFormatCodeFileRoot(t *testing.T) {
	src := "```fern file=m.fern\nbody here\n```\n"
	got := Parse(src).FormatCode(func(code string) (string, bool) {
		return strings.ToUpper(strings.TrimSpace(code)), true
	})
	if !strings.Contains(got, "file=m.fern\nBODY HERE") {
		t.Errorf("file-root body should reformat under its directive fence:\n%s", got)
	}
}

func TestFormatCodeUnclosedFenceKeepsFinalNewline(t *testing.T) {
	src := "intro\n\n```fern\n<<a>>=\nbody\n"
	got := Parse(src).FormatCode(func(code string) (string, bool) {
		return strings.ToUpper(code), true
	})
	if want := "intro\n\n```fern\n<<a>>=\nBODY\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
