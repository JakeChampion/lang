package parser

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/printer"
)

func TestParseUnitLiteral(t *testing.T) {
	prog, err := Parse(`function main(): i32 { let u = (); return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fn := prog.Funcs[0]
	vd, ok := fn.Body.Stmts[0].(*ast.Var)
	if !ok {
		t.Fatalf("first statement is %T, want *ast.Var", fn.Body.Stmts[0])
	}
	if _, ok := vd.Init.(*ast.UnitLit); !ok {
		t.Errorf("initialiser is %T, want *ast.UnitLit", vd.Init)
	}
}

func TestParseEmptyParensStillFunctionTypeBeforeArrow(t *testing.T) {
	prog, err := Parse(`function apply(f: () => i32): i32 { return f(); }
	                    function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pt := prog.Funcs[0].Params[0].Type
	if _, ok := pt.(*ast.FuncType); !ok {
		t.Errorf("parameter type is %T (%s), want *ast.FuncType", pt, pt)
	}
}

func TestFormatUnitLiteralRoundTrips(t *testing.T) {
	src := `function f(): Result[void, IoError] { return Ok(()); }
	        function main(): i32 { return 0; }`
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := printer.Format(prog)
	if !strings.Contains(out, "Ok(())") {
		t.Errorf("formatted output lost the unit literal:\n%s", out)
	}
	if _, err := Parse(out); err != nil {
		t.Errorf("formatted output does not re-parse: %v\n%s", err, out)
	}
}
