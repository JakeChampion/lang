package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// A `for x in f()` over a scalar async stream import iterates LAZILY: the parser
// leaves the ast.ForEach and the checker lowers it to a per-element read loop
// (ast.DesugarForEachStream) — `var c = f$open(); while (true) { if
// (__stream_next(c) == 0) break; var x = __stream_elem_u8(c); BODY }
// __stream_drop(c)`. Output is identical to the eager collect-then-iterate form,
// so this pins the lazy STRUCTURE (the cursor + helper calls + the EOF break)
// that the e2e can't distinguish from eager by its result alone. See
// docs/STREAM-TYPE-SURFACE.md.
func TestStreamForEachDesugarsToLazyLoop(t *testing.T) {
	prog, err := parser.Parse(`@import("test:dep/d", "prod") async function body(): stream[u8];
async function run(): i32 {
	var sum: i32 = 0;
	for x in body() {
		sum = sum + (x as i32);
	}
	return sum;
}
`)
	if err != nil {
		t.Fatal(err)
	}
	// The parser leaves the stream iterand un-lowered (still a ForEach).
	run := funcNamed(t, prog, "run")
	if _, ok := run.Body.Stmts[1].(*ast.ForEach); !ok {
		t.Fatalf("parser should leave a stream for-in as ast.ForEach, got %T", run.Body.Stmts[1])
	}

	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}

	// After Check the ForEach is the lazy block: open, while, drop.
	run = funcNamed(t, prog, "run")
	blk, ok := run.Body.Stmts[1].(*ast.Block)
	if !ok {
		t.Fatalf("stream for-in should lower to a Block, got %T", run.Body.Stmts[1])
	}
	if len(blk.Stmts) != 3 {
		t.Fatalf("expected 3 stmts (open / while / drop), got %d", len(blk.Stmts))
	}
	declC, ok := blk.Stmts[0].(*ast.Var)
	if !ok || !isCallTo(declC.Init, "body$open") {
		t.Fatalf("stmt 0 should be `var c = body$open()`, got %#v", blk.Stmts[0])
	}
	loop, ok := blk.Stmts[1].(*ast.While)
	if !ok {
		t.Fatalf("stmt 1 should be a While, got %T", blk.Stmts[1])
	}
	drop, ok := blk.Stmts[2].(*ast.ExprStmt)
	if !ok || !isCallTo(drop.Expr, "__stream_drop") {
		t.Fatalf("stmt 2 should be `__stream_drop(c)`, got %#v", blk.Stmts[2])
	}
	loopBlk, ok := loop.Body.(*ast.Block)
	if !ok || len(loopBlk.Stmts) < 2 {
		t.Fatalf("loop body should open with EOF-break / bind, got %T", loop.Body)
	}
	// stmt 0: `if (__stream_next(c) == 0) { break; }`
	eofIf, ok := loopBlk.Stmts[0].(*ast.If)
	if !ok {
		t.Fatalf("loop stmt 0 should be the EOF guard `if`, got %T", loopBlk.Stmts[0])
	}
	cmp, ok := eofIf.Cond.(*ast.Binary)
	if !ok || cmp.Op != "==" || !isCallTo(cmp.Left, "__stream_next") {
		t.Fatalf("EOF guard should test `__stream_next(c) == 0`, got %#v", eofIf.Cond)
	}
	// stmt 1: `var x = __stream_elem_u8(c)`
	bindV, ok := loopBlk.Stmts[1].(*ast.Var)
	if !ok || !isCallTo(bindV.Init, "__stream_elem_u8") {
		t.Fatalf("loop stmt 1 should be `var x = __stream_elem_u8(c)`, got %#v", loopBlk.Stmts[1])
	}
}

func funcNamed(t *testing.T, prog *ast.Program, name string) *ast.FuncDecl {
	t.Helper()
	for _, fn := range prog.Funcs {
		if fn.Name == name {
			return fn
		}
	}
	t.Fatalf("function %q not found", name)
	return nil
}

func isCallTo(e ast.Expr, name string) bool {
	call, ok := e.(*ast.Call)
	if !ok {
		return false
	}
	id, ok := call.Callee.(*ast.Ident)
	return ok && id.Name == name
}

// The lazy lowering walks statements, and a nested named function is an
// *ast.FuncDecl STATEMENT whose body is its own block. With no arm for one, the
// ForEach survived Check and reached IR, which has no case for it — the same
// hole the parser's eager desugar had for the same node.
func TestStreamForEachInsideNestedFuncLowers(t *testing.T) {
	prog, err := parser.Parse(`@import("test:dep/d", "prod") async function body(): stream[u8];
async function run(): i32 {
	function inner(): i32 {
		var sum: i32 = 0;
		for x in body() { sum = sum + (x as i32); }
		return sum;
	}
	return inner();
}
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	run := funcNamed(t, prog, "run")
	fd, ok := run.Body.Stmts[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("stmt 0 should be the nested function, got %T", run.Body.Stmts[0])
	}
	if _, still := fd.Body.Stmts[1].(*ast.ForEach); still {
		t.Fatal("stream ForEach survived Check inside the nested function; IR cannot lower one")
	}
	blk, ok := fd.Body.Stmts[1].(*ast.Block)
	if !ok {
		t.Fatalf("stream for-in should lower to a Block, got %T", fd.Body.Stmts[1])
	}
	declC, ok := blk.Stmts[0].(*ast.Var)
	if !ok || !isCallTo(declC.Init, "body$open") {
		t.Fatalf("stmt 0 should be `var c = body$open()`, got %#v", blk.Stmts[0])
	}
}
