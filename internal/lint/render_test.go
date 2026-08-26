package lint_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/lint"
)

const renderSrc = "function noisy(n: i32): i32 {\n    return n;\n}\n"

func srcOf(string) string { return renderSrc }

func TestRenderLayout(t *testing.T) {
	defer diag.SetColor(diag.SetColor(false))

	f := lint.Finding{
		Rule:     "cyclomatic-complexity",
		Severity: lint.Warn,
		Pos:      ast.Position{Line: 1, Col: 1},
		File:     "a/b.fern",
		Msg:      "function `noisy` is too complex",
		Help:     "split it up",
	}
	got := lint.Render(f, srcOf)
	want := "a/b.fern:1:1: warning[cyclomatic-complexity]: function `noisy` is too complex\n" +
		"    function noisy(n: i32): i32 {\n" +
		"    ^\n" +
		"    help: split it up"
	if got != want {
		t.Errorf("render =\n%s\n\nwant\n%s", got, want)
	}

	// Severity picks the header word, so a `deny` run reads like the
	// build failure it is.
	f.Severity = lint.Deny
	if !strings.HasPrefix(lint.Render(f, srcOf), "a/b.fern:1:1: error[cyclomatic-complexity]:") {
		t.Errorf("deny finding did not render as an error: %s", lint.Render(f, srcOf))
	}
}

// Colour is off for every non-interactive caller, and the plain rendering
// must stay byte-for-byte free of escapes.
func TestRenderColour(t *testing.T) {
	f := lint.Finding{Rule: "r", Severity: lint.Warn, Pos: ast.Position{Line: 1, Col: 1}, File: "a.fern", Msg: "m"}

	defer diag.SetColor(diag.SetColor(false))
	if plain := lint.Render(f, srcOf); strings.Contains(plain, "\x1b[") {
		t.Errorf("colour-off render carries escapes: %q", plain)
	}
	diag.SetColor(true)
	if coloured := lint.Render(f, srcOf); !strings.Contains(coloured, "\x1b[") {
		t.Error("colour-on render carries no escapes")
	}
}

// A position pointing past the end of the file (or a caller with no source
// to hand) must degrade to the header, not invent context or panic.
func TestRenderWithoutSource(t *testing.T) {
	defer diag.SetColor(diag.SetColor(false))
	f := lint.Finding{Rule: "r", Severity: lint.Warn, Pos: ast.Position{Line: 99, Col: 400}, File: "a.fern", Msg: "m", Help: "h"}
	got := lint.Render(f, srcOf)
	if strings.Count(got, "\n") != 1 || !strings.Contains(got, "help: h") {
		t.Errorf("render = %q, want the header plus the help line only", got)
	}
	if got := lint.Render(f, nil); strings.Contains(got, "\n    function") {
		t.Errorf("render with no source accessor quoted a line: %q", got)
	}
}

// A column past the end of the line must clamp to just past the line's
// last character — the end-of-line caret — rather than pad off into space.
func TestRenderClampsCaret(t *testing.T) {
	defer diag.SetColor(diag.SetColor(false))
	f := lint.Finding{Rule: "r", Severity: lint.Warn, Pos: ast.Position{Line: 2, Col: 999}, File: "a.fern", Msg: "m"}
	lines := strings.Split(lint.Render(f, srcOf), "\n")
	if len(lines) != 3 {
		t.Fatalf("render = %q", lines)
	}
	if len(lines[2]) > len(lines[1])+1 {
		t.Errorf("caret line %q runs past the end of the source line %q", lines[2], lines[1])
	}
}

func TestSummary(t *testing.T) {
	cases := []struct {
		name string
		fs   []lint.Finding
		want string
	}{
		{"nothing", nil, ""},
		{"one warning", []lint.Finding{{Severity: lint.Warn}}, "1 warning generated"},
		{"two warnings", []lint.Finding{{Severity: lint.Warn}, {Severity: lint.Warn}}, "2 warnings generated"},
		{"one error", []lint.Finding{{Severity: lint.Deny}}, "1 error generated"},
		{"both", []lint.Finding{{Severity: lint.Deny}, {Severity: lint.Warn}}, "1 error and 1 warning generated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lint.Summary(tc.fs); got != tc.want {
				t.Errorf("summary = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderAllSeparatesFindings(t *testing.T) {
	defer diag.SetColor(diag.SetColor(false))
	fs := []lint.Finding{
		{Rule: "r", Severity: lint.Warn, Pos: ast.Position{Line: 1, Col: 1}, File: "a.fern", Msg: "first"},
		{Rule: "r", Severity: lint.Warn, Pos: ast.Position{Line: 2, Col: 1}, File: "a.fern", Msg: "second"},
	}
	got := lint.RenderAll(fs, srcOf)
	if !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Fatalf("render = %q", got)
	}
	if !strings.Contains(got, "\n\n") {
		t.Error("findings must be separated by a blank line")
	}
	if !strings.HasSuffix(got, "\n") {
		t.Error("output must end in a newline")
	}
	if lint.RenderAll(nil, srcOf) != "" {
		t.Error("no findings must render as nothing at all")
	}
}
