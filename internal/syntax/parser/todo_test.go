package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func firstStmtOfFunc(t *testing.T, prog *ast.Program, name string) ast.Stmt {
	t.Helper()
	for _, fn := range prog.Funcs {
		if fn.Name == name {
			if fn.Body == nil || len(fn.Body.Stmts) == 0 {
				t.Fatalf("%s has empty body", name)
			}
			return fn.Body.Stmts[0]
		}
	}
	t.Fatalf("function %s not found", name)
	return nil
}

func requireTodoBlock(t *testing.T, s ast.Stmt, wantMsg bool) *ast.Block {
	t.Helper()
	body, ok := s.(*ast.Block)
	if !ok {
		t.Fatalf("stmt = %T, want *ast.Block", s)
	}
	if !body.IsTodo {
		t.Fatalf("Block.IsTodo = false, want true")
	}
	if (body.TodoMsg != nil) != wantMsg {
		t.Fatalf("Block.TodoMsg present = %v, want %v", body.TodoMsg != nil, wantMsg)
	}
	if len(body.Stmts) != 2 {
		t.Fatalf("todo body has %d stmts, want 2", len(body.Stmts))
	}
	es0, ok := body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("body[0] = %T, want *ast.ExprStmt", body.Stmts[0])
	}
	call0, ok := es0.Expr.(*ast.Call)
	if !ok {
		t.Fatalf("body[0] expr = %T, want *ast.Call", es0.Expr)
	}
	if id, ok := call0.Callee.(*ast.Ident); !ok || id.Name != "eprint" || !id.Builtin {
		t.Fatalf("body[0] callee = %#v, want the builtin eprint", call0.Callee)
	}
	es1, ok := body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("body[1] = %T, want *ast.ExprStmt", body.Stmts[1])
	}
	call1, ok := es1.Expr.(*ast.Call)
	if !ok {
		t.Fatalf("body[1] expr = %T, want *ast.Call", es1.Expr)
	}
	if id, ok := call1.Callee.(*ast.Ident); !ok || id.Name != "exit" || !id.Builtin {
		t.Fatalf("body[1] callee = %#v, want the builtin exit", call1.Callee)
	}
	num, ok := call1.Args[0].(*ast.NumberLit)
	if !ok || num.Value != 101 {
		t.Fatalf("exit arg = %v, want 101", call1.Args[0])
	}
	return body
}

func TestParseTodoBare(t *testing.T) {
	prog, err := Parse(`function f(): i32 { todo; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	requireTodoBlock(t, firstStmtOfFunc(t, prog, "f"), false)
	if len(prog.TodoSites) != 1 {
		t.Fatalf("TodoSites = %d entries, want 1", len(prog.TodoSites))
	}
	if prog.TodoSites[0].Line != 1 {
		t.Errorf("TodoSites[0].Line = %d, want 1", prog.TodoSites[0].Line)
	}
}

func TestParseTodoWithMessage(t *testing.T) {
	prog, err := Parse(`function f(): i32 { todo("wide-K maps"); }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	body := requireTodoBlock(t, firstStmtOfFunc(t, prog, "f"), true)
	msg, ok := body.TodoMsg.(*ast.StringLit)
	if !ok || msg.Value != "wide-K maps" {
		t.Fatalf("TodoMsg = %#v, want StringLit \"wide-K maps\"", body.TodoMsg)
	}
	call := body.Stmts[0].(*ast.ExprStmt).Expr.(*ast.Call)
	bin, ok := call.Args[0].(*ast.Binary)
	if !ok || bin.Op != "+" {
		t.Fatalf("eprint arg = %#v, want Binary(+)", call.Args[0])
	}
	if lit, ok := bin.Left.(*ast.StringLit); !ok || lit.Value != "todo: " {
		t.Fatalf("eprint prefix = %#v, want \"todo: \"", bin.Left)
	}
}

func TestParseTodoEmptyParens(t *testing.T) {
	prog, err := Parse(`function f(): i32 { todo(); }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	requireTodoBlock(t, firstStmtOfFunc(t, prog, "f"), false)
}

func TestTodoUsableAsIdentifier(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
    let todo: i32 = 5;
    todo = todo + 1;
    return todo * 2;
}`)
	if err != nil {
		t.Fatalf("`todo` as an identifier should parse: %v", err)
	}
	if len(prog.TodoSites) != 0 {
		t.Errorf("TodoSites = %d entries, want 0 (no stub statements)", len(prog.TodoSites))
	}
}
