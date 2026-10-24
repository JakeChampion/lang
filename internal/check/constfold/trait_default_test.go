package constfold

import (
	"github.com/jakechampion/lang/internal/syntax/ast"
	"testing"
)

func TestConstFoldsInsideTraitDefaults(t *testing.T) {
	cases := []struct {
		name, params, body string
		wantLiteral        bool
	}{
		{"constant", "", "return LIMIT;", true},
		{"parameter-shadow", "LIMIT: i32", "return LIMIT;", false},
		{"local-shadow", "", "let LIMIT: i32 = 11; return LIMIT;", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := fold(t, "const LIMIT: i32 = 7; trait Count { function count("+tc.params+"): i32 { "+tc.body+" } } function main(): i32 { return 0; }")
			body := prog.Traits[0].Methods[0].Body
			ret := body.Stmts[len(body.Stmts)-1].(*ast.Return)
			_, literal := ret.Value.(*ast.NumberLit)
			if literal != tc.wantLiteral {
				t.Fatalf("return expression is %T, want literal %v", ret.Value, tc.wantLiteral)
			}
			if literal && ret.Value.(*ast.NumberLit).Value != 7 {
				t.Fatalf("folded value: %#v", ret.Value)
			}
		})
	}
}
