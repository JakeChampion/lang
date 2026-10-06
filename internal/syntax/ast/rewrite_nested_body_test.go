package ast

import "testing"

// RewriteProgramExprs must descend into the body of a NESTED function and of a
// Lambda, not just top-level function bodies. The checker's post-check pass
// uses it to splice in every desugar it built during checking — checked
// arithmetic (Binary.CheckedLowered), composite operator overloads (ArithCall /
// NegCall / EqCall / CmpCall), the error-converting `?` (TryOp.Lowered), and
// the default-i32 width stamp.
//
// Skipping those bodies did not lose the rewrite quietly: both the IR and the
// interpreter reject the un-desugared node, so `a +? b` or an overloaded `+`
// inside a nested function was a hard error rather than a wrong answer. They
// differ only in WHEN — the IR rejects it at emit whether the code would run
// or not, while the interpreter reaches it only if the nested function is
// called.
//
// Found by the drop-guided differential sweep once fernsmith started
// generating checked operators inside its nested-function production.
func TestRewriteProgramExprsDescendsIntoNestedFunctionBody(t *testing.T) {
	inner := &FuncDecl{
		Name: "inner",
		Body: &Block{Stmts: []Stmt{
			&Return{Value: &Ident{Name: "target"}},
		}},
	}
	prog := &Program{Funcs: []*FuncDecl{{
		Name: "outer",
		Body: &Block{Stmts: []Stmt{
			inner,
			&Return{Value: &Ident{Name: "other"}},
		}},
	}}}

	seen := map[string]bool{}
	RewriteProgramExprs(prog, func(e Expr) Expr {
		if id, ok := e.(*Ident); ok {
			seen[id.Name] = true
			if id.Name == "target" {
				return &Ident{Name: "rewritten"}
			}
		}
		return e
	})

	if !seen["target"] {
		t.Error("the nested function's body was never visited")
	}
	if !seen["other"] {
		t.Error("the enclosing function's body was never visited")
	}
	ret, ok := inner.Body.Stmts[0].(*Return)
	if !ok {
		t.Fatalf("nested body stmt = %T, want *Return", inner.Body.Stmts[0])
	}
	if id, ok := ret.Value.(*Ident); !ok || id.Name != "rewritten" {
		t.Errorf("nested return value = %#v, want the rewritten Ident", ret.Value)
	}
}

// The same for a `loop { … }` body. `loop` is its own statement kind rather
// than sugar over While, so a rewriter that lists the loop forms by hand can
// omit it and leave the body un-desugared — `a +? b` inside a `loop` was
// rejected by both engines ("+?" not supported) while the identical code
// inside a `while (true)` ran.
func TestRewriteProgramExprsDescendsIntoLoopBody(t *testing.T) {
	body := &Block{Stmts: []Stmt{
		&ExprStmt{Expr: &Ident{Name: "target"}},
	}}
	prog := &Program{Funcs: []*FuncDecl{{
		Name: "outer",
		Body: &Block{Stmts: []Stmt{&Loop{Body: body}}},
	}}}

	visited := false
	RewriteProgramExprs(prog, func(e Expr) Expr {
		if id, ok := e.(*Ident); ok && id.Name == "target" {
			visited = true
			return &Ident{Name: "rewritten"}
		}
		return e
	})

	if !visited {
		t.Fatal("the loop body was never visited")
	}
	es := body.Stmts[0].(*ExprStmt)
	if id, ok := es.Expr.(*Ident); !ok || id.Name != "rewritten" {
		t.Errorf("loop body expr = %#v, want the rewritten Ident", es.Expr)
	}
}

// The same for an anonymous function expression, which the rewriter reaches
// before closureconv hoists it into a top-level FuncDecl.
func TestRewriteProgramExprsDescendsIntoLambdaBody(t *testing.T) {
	lam := &Lambda{
		Body: &Block{Stmts: []Stmt{
			&Return{Value: &Ident{Name: "target"}},
		}},
	}
	prog := &Program{Funcs: []*FuncDecl{{
		Name: "outer",
		Body: &Block{Stmts: []Stmt{
			&Var{Name: "f", Init: lam},
		}},
	}}}

	visited := false
	RewriteProgramExprs(prog, func(e Expr) Expr {
		if id, ok := e.(*Ident); ok && id.Name == "target" {
			visited = true
			return &Ident{Name: "rewritten"}
		}
		return e
	})

	if !visited {
		t.Fatal("the lambda's body was never visited")
	}
	ret := lam.Body.Stmts[0].(*Return)
	if id, ok := ret.Value.(*Ident); !ok || id.Name != "rewritten" {
		t.Errorf("lambda return value = %#v, want the rewritten Ident", ret.Value)
	}
}
