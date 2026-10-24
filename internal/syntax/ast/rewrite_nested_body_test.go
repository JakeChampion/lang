package ast

import "testing"

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
