package parser

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/printer"
)

// `()` is the unit value — the sole inhabitant of `void`, and the only way
// to write one. It exists so a generic that has nothing interesting to
// carry can still be constructed: `Result[void, IoError]` is what every
// fallible operation with no result returns, and `Ok(())` builds it.
func TestParseUnitLiteral(t *testing.T) {
	prog, err := Parse(`function main(): i32 { var u = (); return 0; }`)
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

// `()` in TYPE position is void's other spelling, so the unit value's type
// can be written where it reads best: `Result[(), IoError]` rather than
// `Result[void, IoError]`. Both parse to the same VoidType.
func TestParseUnitType(t *testing.T) {
	prog, err := Parse(`function f(): Result[(), IoError] { return Ok(()); }
	                    function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, ok := prog.Funcs[0].ReturnType.(ast.EnumType)
	if !ok {
		t.Fatalf("result type is %T, want ast.EnumType", prog.Funcs[0].ReturnType)
	}
	if len(res.Args) != 2 {
		t.Fatalf("Result has %d args, want 2", len(res.Args))
	}
	if _, ok := res.Args[0].(ast.VoidType); !ok {
		t.Errorf("first type argument is %T (%s), want ast.VoidType", res.Args[0], res.Args[0])
	}
}

// The zero-argument FUNCTION type still wins the same token shape: `()` is
// only the unit type when no `=>` follows it.
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

// The formatter has to render the literal back — a unit value that
// round-trips to nothing would silently delete the payload. `Format` is
// the formatter behind `fern -fmt` and the LSP; `printer.Print` is a
// determinism-test helper whose type printer predates generic enums and
// renders `Result[…]` as empty, so it is not the one to assert on here.
func TestFormatUnitLiteralRoundTrips(t *testing.T) {
	src := `function f(): Result[(), IoError] { return Ok(()); }
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
