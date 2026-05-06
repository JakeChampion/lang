package diag

import (
	"errors"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

type fakeErr struct {
	pos ast.Position
	msg string
}

func (e *fakeErr) Error() string          { return "type error at " + e.pos.String() + ": " + e.msg }
func (e *fakeErr) Position() ast.Position { return e.pos }

type fakeSpanErr struct {
	fakeErr
	span int
}

func (e *fakeSpanErr) Length() int { return e.span }

type fakeHintErr struct {
	fakeErr
	hint string
}

func (e *fakeHintErr) Hint() string { return e.hint }

func TestFormatRendersSnippetAndCaret(t *testing.T) {
	src := "function f() {\n    return x + 1;\n}\n"
	out := Format("", src, &fakeErr{pos: ast.Position{Line: 2, Col: 12}, msg: "undefined identifier \"x\""})
	want := "2:12: error: undefined identifier \"x\"\n    " +
		"    return x + 1;\n" +
		"               ^"
	if out != want {
		t.Errorf("rendered:\n%s\n--- want ---\n%s", out, want)
	}
}

func TestFormatIncludesFilename(t *testing.T) {
	src := "function f() {}\n"
	out := Format("foo.lang", src, &fakeErr{pos: ast.Position{Line: 1, Col: 1}, msg: "boom"})
	if !strings.HasPrefix(out, "foo.lang:1:1: error: boom\n") {
		t.Errorf("expected filename prefix in:\n%s", out)
	}
}

func TestFormatSpanRendersSquiggle(t *testing.T) {
	src := "var hello = 1;\n"
	e := &fakeSpanErr{
		fakeErr: fakeErr{pos: ast.Position{Line: 1, Col: 5}, msg: "bad"},
		span:    5, // "hello"
	}
	out := Format("", src, e)
	if !strings.Contains(out, "    ^~~~") {
		t.Errorf("expected ^~~~ span, got:\n%s", out)
	}
}

func TestFormatSpanCappedToLine(t *testing.T) {
	// span larger than the line shouldn't run off the right edge.
	src := "abc\n"
	e := &fakeSpanErr{
		fakeErr: fakeErr{pos: ast.Position{Line: 1, Col: 1}, msg: "bad"},
		span:    99,
	}
	out := Format("", src, e)
	last := out[strings.LastIndex(out, "\n")+1:]
	if len(strings.TrimSpace(last)) > len("abc") {
		t.Errorf("squiggle ran past EOL: %q", last)
	}
}

func TestFormatHintRenderedAsNote(t *testing.T) {
	src := "x\n"
	e := &fakeHintErr{
		fakeErr: fakeErr{pos: ast.Position{Line: 1, Col: 1}, msg: "bad"},
		hint:    `did you mean "y"?`,
	}
	out := Format("", src, e)
	if !strings.Contains(out, `note: did you mean "y"?`) {
		t.Errorf("expected note line, got:\n%s", out)
	}
}

func TestFormatPlainErrorFallback(t *testing.T) {
	out := Format("", "source", errors.New("boom"))
	if out != "boom" {
		t.Errorf("got %q, want %q", out, "boom")
	}
}

func TestErrorsAggregates(t *testing.T) {
	es := Errors{
		&fakeErr{pos: ast.Position{Line: 1, Col: 1}, msg: "first"},
		&fakeErr{pos: ast.Position{Line: 2, Col: 1}, msg: "second"},
	}
	out := Format("", "a\nb\n", es)
	if !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Errorf("expected both errors, got:\n%s", out)
	}
}

func TestPickLine(t *testing.T) {
	src := "alpha\nbeta\ngamma"
	if l := pickLine(src, 2); l != "beta" {
		t.Errorf("line 2 = %q, want \"beta\"", l)
	}
	if l := pickLine(src, 3); l != "gamma" {
		t.Errorf("line 3 (no trailing newline) = %q, want \"gamma\"", l)
	}
	if l := pickLine(src, 99); l != "" {
		t.Errorf("line 99 should be empty, got %q", l)
	}
}

func TestTabsBecomeSpaces(t *testing.T) {
	src := "\tcode here\n"
	out := pickLine(src, 1)
	if strings.Contains(out, "\t") {
		t.Errorf("expected tabs replaced, got %q", out)
	}
}

func TestSuggestPicksClosest(t *testing.T) {
	cands := []string{"length", "left", "lenght"}
	if got := Suggest("lenght", cands); got != "length" {
		// "lenght" is in the candidate list but Suggest skips exact matches,
		// so the next-closest is "length" (edit distance 2 → swap of n/g).
		t.Errorf("got %q, want %q", got, "length")
	}
}

func TestSuggestReturnsEmptyOutsideBudget(t *testing.T) {
	if got := Suggest("foobar", []string{"completelyDifferent"}); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestSuggestShortNamesUseTighterBudget(t *testing.T) {
	// "ab" → "yz" has distance 2; for short names we only allow 1.
	if got := Suggest("ab", []string{"yz"}); got != "" {
		t.Errorf("got %q, want empty for short name above budget", got)
	}
	if got := Suggest("ab", []string{"ax"}); got != "ax" {
		t.Errorf("got %q, want \"ax\"", got)
	}
}
