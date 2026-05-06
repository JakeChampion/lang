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

func TestFormatRendersSnippetAndCaret(t *testing.T) {
	src := "function f() {\n    return x + 1;\n}\n"
	out := Format(src, &fakeErr{pos: ast.Position{Line: 2, Col: 12}, msg: "undefined identifier \"x\""})
	want := "error at 2:12: undefined identifier \"x\"\n    " +
		"    return x + 1;\n" +
		"               ^"
	if out != want {
		t.Errorf("rendered:\n%s\n--- want ---\n%s", out, want)
	}
}

func TestFormatPlainErrorFallback(t *testing.T) {
	out := Format("source", errors.New("boom"))
	if out != "boom" {
		t.Errorf("got %q, want %q", out, "boom")
	}
}

func TestErrorsAggregates(t *testing.T) {
	es := Errors{
		&fakeErr{pos: ast.Position{Line: 1, Col: 1}, msg: "first"},
		&fakeErr{pos: ast.Position{Line: 2, Col: 1}, msg: "second"},
	}
	out := Format("a\nb\n", es)
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
