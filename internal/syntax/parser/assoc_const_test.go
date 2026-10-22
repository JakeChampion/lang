package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// An associated constant parses in a trait body (abstract, or with a
// default) and in an impl body, each as an associated function with no
// parameters marked const; `f64::LANES` parses as a path on the primitive
// keyword, which the formatter round-trips (see docs/ASSOCIATED-TYPES.md).
func TestAssocConstParse(t *testing.T) {
	prog, err := Parse(`trait Lanes {
    const LANES: i32;
    const NAME: string = "lanes";
    function width(self: Self): i32;
}
struct V2 { a: f64 }
impl Lanes for V2 {
    const LANES: i32 = 2;
    function width(self: Self): i32 { return Self::LANES; }
}
function chunk[T: Lanes](n: i32): i32 { return n / T::LANES + f64::LANES; }`)
	if err != nil {
		t.Fatal(err)
	}
	tr := prog.Traits[0]
	if len(tr.Methods) != 3 {
		t.Fatalf("trait methods = %d, want 3", len(tr.Methods))
	}
	if m := tr.Methods[0]; !m.Const || !m.Assoc || m.Name != "LANES" || m.Body != nil || len(m.Params) != 0 {
		t.Errorf("LANES = %+v, want an abstract const", m)
	}
	if m := tr.Methods[1]; !m.Const || !m.Assoc || m.Name != "NAME" || m.Body == nil {
		t.Errorf("NAME = %+v, want a defaulted const", m)
	}
	if m := tr.Methods[2]; m.Const || m.Assoc {
		t.Errorf("width = %+v, want an ordinary method", m)
	}
	var bound *ast.FuncDecl
	for _, fn := range prog.Funcs {
		if fn.Name == "LANES" {
			bound = fn
		}
	}
	if bound == nil || !bound.AssocConst || bound.AssocType != "V2" || len(bound.Params) != 0 || bound.Body == nil {
		t.Fatalf("impl const = %+v, want an AssocConst associated function on V2", bound)
	}
	if _, ok := bound.ReturnType.(ast.NumberType); !ok {
		t.Errorf("impl const type = %T, want i32", bound.ReturnType)
	}
	ret := prog.Funcs[len(prog.Funcs)-1].Body.Stmts[0].(*ast.Return).Value.(*ast.Binary)
	div := ret.Left.(*ast.Binary)
	if fa, ok := div.Right.(*ast.FieldAccess); !ok || !fa.PathSep || fa.Field != "LANES" {
		t.Errorf("T::LANES = %#v, want a PathSep field access", div.Right)
	}
	if fa, ok := ret.Right.(*ast.FieldAccess); !ok || !fa.PathSep || fa.Field != "LANES" {
		t.Errorf("f64::LANES = %#v, want a PathSep field access on the keyword", ret.Right)
	} else if id, ok := fa.Target.(*ast.Ident); !ok || id.Name != "f64" {
		t.Errorf("f64::LANES target = %#v, want the f64 ident", fa.Target)
	}
}

// An impl's const needs its value; a trait's abstract const takes none.
func TestAssocConstParseErrors(t *testing.T) {
	for _, src := range []string{
		`trait Lanes { const LANES: i32; }
struct V2 { a: f64 }
impl Lanes for V2 { const LANES: i32; }`,
		`trait Lanes { const LANES = 2; }`,
	} {
		if _, err := Parse(src); err == nil {
			t.Errorf("expected a parse error for %q", src)
		}
	}
}
