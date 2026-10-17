package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
)

func TestDuplicateVariableHasPreviousDeclarationLabel(t *testing.T) {
	err := checkSource(t, "function main(): i32 {\n  let n = 1;\n  let n = 2;\n  let n = 3;\n  return n;\n}\n")
	errs, ok := err.(diag.Errors)
	if !ok || len(errs) != 2 {
		t.Fatalf("wanted two duplicate declarations, got %v", err)
	}
	for i, raw := range errs {
		e, ok := raw.(*Error)
		if !ok || e.Code() != "E013" {
			t.Fatalf("unexpected diagnostic %v", raw)
		}
		labels := e.Labels()
		if len(labels) != 2 || labels[0].Kind != diag.LabelPrimary || labels[1].Kind != diag.LabelSecondary || labels[1].Pos.Line != i+2 || labels[1].Pos.Col != 3 || labels[1].Message != "previous declaration is here" {
			t.Fatalf("duplicate %d has labels %#v", i, labels)
		}
	}
	if err := checkSource(t, "function main(): i32 { let n = 1; { let n = 2; } return n; }"); err != nil {
		t.Fatalf("nested shadowing should remain legal: %v", err)
	}
}
