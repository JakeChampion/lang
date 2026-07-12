package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// `todo;` / `todo("msg");` is a parser-level stub statement that desugars to
// `loop { eprint("todo[: msg]"); exit(101); }` (see parseTodo). The `loop`
// wrapper makes the stub diverge for the checker's missing-return (E052) and
// `let else` analyses; the IsTodo/TodoMsg fields let the formatter re-print
// the sugar. These tests pin the desugar shape, the site recording, and the
// contextual-intercept rule (`todo` stays a usable identifier).

// firstStmtOfFunc returns the first statement of the named function's body.
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

// requireTodoLoop asserts the statement is the todo desugar: an IsTodo Loop
// whose body is `eprint(<text>); exit(101);`.
func requireTodoLoop(t *testing.T, s ast.Stmt, wantMsg bool) *ast.Loop {
	t.Helper()
	lp, ok := s.(*ast.Loop)
	if !ok {
		t.Fatalf("stmt = %T, want *ast.Loop", s)
	}
	if !lp.IsTodo {
		t.Fatalf("Loop.IsTodo = false, want true")
	}
	if (lp.TodoMsg != nil) != wantMsg {
		t.Fatalf("Loop.TodoMsg present = %v, want %v", lp.TodoMsg != nil, wantMsg)
	}
	body, ok := lp.Body.(*ast.Block)
	if !ok || len(body.Stmts) != 2 {
		t.Fatalf("todo body = %T with %d stmts, want *ast.Block with 2", lp.Body, len(body.Stmts))
	}
	// Stmt 0: eprint(...)
	es0, ok := body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("body[0] = %T, want *ast.ExprStmt", body.Stmts[0])
	}
	call0, ok := es0.Expr.(*ast.Call)
	if !ok {
		t.Fatalf("body[0] expr = %T, want *ast.Call", es0.Expr)
	}
	if id, ok := call0.Callee.(*ast.Ident); !ok || id.Name != "eprint" {
		t.Fatalf("body[0] callee = %v, want eprint", call0.Callee)
	}
	// Stmt 1: exit(101)
	es1, ok := body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("body[1] = %T, want *ast.ExprStmt", body.Stmts[1])
	}
	call1, ok := es1.Expr.(*ast.Call)
	if !ok {
		t.Fatalf("body[1] expr = %T, want *ast.Call", es1.Expr)
	}
	if id, ok := call1.Callee.(*ast.Ident); !ok || id.Name != "exit" {
		t.Fatalf("body[1] callee = %v, want exit", call1.Callee)
	}
	num, ok := call1.Args[0].(*ast.NumberLit)
	if !ok || num.Value != 101 {
		t.Fatalf("exit arg = %v, want 101", call1.Args[0])
	}
	return lp
}

func TestParseTodoBare(t *testing.T) {
	prog, err := Parse(`function f(): i32 { todo; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	requireTodoLoop(t, firstStmtOfFunc(t, prog, "f"), false)
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
	lp := requireTodoLoop(t, firstStmtOfFunc(t, prog, "f"), true)
	msg, ok := lp.TodoMsg.(*ast.StringLit)
	if !ok || msg.Value != "wide-K maps" {
		t.Fatalf("TodoMsg = %#v, want StringLit \"wide-K maps\"", lp.TodoMsg)
	}
	// The eprint text is "todo: " + msg (a Binary), pinning the runtime
	// message shape the self-host parser must match byte-for-byte.
	body := lp.Body.(*ast.Block)
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
	// `todo();` is the bare form with parens — same desugar, no message.
	prog, err := Parse(`function f(): i32 { todo(); }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	requireTodoLoop(t, firstStmtOfFunc(t, prog, "f"), false)
}

func TestTodoUsableAsIdentifier(t *testing.T) {
	// Only the statement-position `todo ;` / `todo (` shapes are
	// intercepted — `todo` as a variable name, in expressions, and as an
	// assignment target must keep parsing as an ordinary identifier.
	prog, err := Parse(`function f(): i32 {
    var todo: i32 = 5;
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
