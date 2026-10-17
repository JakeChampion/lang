package diag

import (
	"errors"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

type terseLabeledErr struct{ fakeLabeledErr }

func (e *terseLabeledErr) Code() string { return "E013" }
func (e *terseLabeledErr) Suggestion() *Suggestion {
	return &Suggestion{Pos: e.pos, Length: 1, Replacement: "other", Title: "rename it"}
}

func TestTerseDiagnosticsKeepOneLinePerError(t *testing.T) {
	defer SetColor(SetColor(true))
	defer SetTerse(SetTerse(true))
	e := &terseLabeledErr{fakeLabeledErr{
		fakeErr: fakeErr{pos: ast.Position{Line: 1, Col: 5}, msg: "duplicate\nmore"},
		labels:  []Label{{Pos: ast.Position{Line: 1, Col: 5}, Kind: LabelPrimary}, {Pos: ast.Position{Line: 1, Col: 1}, Message: "previous declaration", Kind: LabelSecondary}},
	}}
	out := Format("bad\nname.fern", "let n = 1;", Errors{e, errors.New("second\r\nmessage")})
	want := "bad\\nname.fern:1:5: error[E013]: duplicate\\nmore\nsecond\\r\\nmessage"
	if out != want || strings.Contains(out, "\x1b[") || strings.Contains(out, "help:") || strings.Contains(out, "note:") {
		t.Fatalf("terse output = %q, want %q", out, want)
	}
}
