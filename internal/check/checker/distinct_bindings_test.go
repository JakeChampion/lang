package checker

import (
	"slices"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

const repeatedBindings = `enum Pair { Two(i32, i32) }
function f(own w: i32[], p: Option[i32[]], q: Pair): i32 {
    let n = match (p) { Some(w) => w.len(), None => 0 };
    let m = match (q) { Two(w, t) => w + t };
    for w in [1, 2] { n = n + w; }
    if (n > 0) { let t = 1; n = n + t; }
    let t = (w: i32): i32 => w + 1;
    let (a, w) = (1, 2);
    return n + m + t(a) + w;
}`

// bindingNames lists every name that fn binds or reads, in walk order.
func bindingNames(fn *ast.FuncDecl) []string {
	var out []string
	for _, p := range fn.Params {
		out = append(out, p.Name)
	}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			out = append(out, x.Name)
		case *ast.Var:
			out = append(out, x.Name)
		case *ast.Destructure:
			out = append(out, x.Names...)
		case *ast.ForEach:
			out = append(out, x.Var)
		case *ast.Match:
			for _, a := range x.Arms {
				out = append(out, a.Binders()...)
			}
		case *ast.MatchExpr:
			for _, a := range x.Arms {
				out = append(out, a.Binders()...)
			}
		case *ast.Lambda:
			for _, p := range x.Params {
				out = append(out, p.Name)
			}
		}
		return true
	})
	return out
}

func TestDistinctBindingsNamesEachBindingAndRestores(t *testing.T) {
	prog, err := parser.Parse(repeatedBindings)
	if err != nil {
		t.Fatal(err)
	}
	fn := prog.Funcs[len(prog.Funcs)-1]
	before := bindingNames(fn)
	restore := distinctBindings(fn)
	renamed := bindingNames(fn)
	for _, want := range []string{"w#1", "w#2", "w#3", "w#4", "w#5", "t#1", "t#2"} {
		if !slices.Contains(renamed, want) {
			t.Errorf("no binding renamed to %q in %v", want, renamed)
		}
	}
	restore()
	if after := bindingNames(fn); !slices.Equal(before, after) {
		t.Fatalf("names not restored:\nbefore %v\nafter  %v", before, after)
	}
}
