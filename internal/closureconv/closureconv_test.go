package closureconv_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/closureconv"
	"github.com/jakechampion/lang/internal/parser"
)

// runConvert parses, type-checks, then runs closureconv.
// Returns the post-conversion program. closureconv mutates in
// place; the returned pointer is the same as prog.
func runConvert(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := closureconv.Convert(prog, info); err != nil {
		t.Fatalf("convert: %v", err)
	}
	return prog
}

// findVarStmt returns the first *ast.Var stmt named `name` in
// blk, or nil if not found. Helper for poking at the
// post-conversion def site.
func findVarStmt(blk *ast.Block, name string) *ast.Var {
	if blk == nil {
		return nil
	}
	for _, s := range blk.Stmts {
		if v, ok := s.(*ast.Var); ok && v.Name == name {
			return v
		}
	}
	return nil
}

// TestConvertHoistsSimpleLocalFunc — a nested function with no
// captures becomes a top-level decl. The def site is replaced
// with a `var <name> = MakeClosure{...}`. The hoisted decl's
// name is generated (`__closure_<orig>_<N>`), not the same as
// the source-level name.
func TestConvertHoistsSimpleLocalFunc(t *testing.T) {
	src := `function main(): i32 {
		function bump(x: i32): i32 { return x + 1; }
		return bump(41);
	}`
	prog := runConvert(t, src)
	// The hoisted FuncDecl should appear in prog.Funcs alongside
	// main, named `__closure_bump_<N>`.
	var sawMain, sawHoisted bool
	for _, fn := range prog.Funcs {
		if fn.Name == "main" {
			sawMain = true
			continue
		}
		if strings.HasPrefix(fn.Name, "__closure_bump_") {
			sawHoisted = true
		}
	}
	if !sawMain {
		t.Error("main should still be in prog.Funcs")
	}
	if !sawHoisted {
		t.Error("hoisted clone `__closure_bump_*` not found in prog.Funcs")
	}
	// Original `function bump(...)` statement should be
	// replaced with `var bump = MakeClosure{...}`.
	mainFn := findFuncByName(prog, "main")
	if mainFn == nil {
		t.Fatal("main vanished")
	}
	bumpDef := findVarStmt(mainFn.Body, "bump")
	if bumpDef == nil {
		t.Fatal("def site `var bump = ...` not found in main's body")
	}
	if _, ok := bumpDef.Init.(*ast.MakeClosure); !ok {
		t.Errorf("bump's def init: expected *ast.MakeClosure, got %T", bumpDef.Init)
	}
}

// TestConvertRewritesCapturedRefsAsCaptureRef — an Ident that
// refers to an outer-scope local becomes a *ast.CaptureRef in
// the hoisted body. Codegen lowers CaptureRef as a load from
// the env arg.
func TestConvertRewritesCapturedRefsAsCaptureRef(t *testing.T) {
	src := `function main(): i32 {
		var n: i32 = 100;
		function bump(x: i32): i32 { return x + n; }
		return bump(5);
	}`
	prog := runConvert(t, src)
	// Find the hoisted bump clone — named `__closure_bump_<N>`.
	var bumpClone *ast.FuncDecl
	for _, fn := range prog.Funcs {
		if strings.HasPrefix(fn.Name, "__closure_bump_") {
			bumpClone = fn
			break
		}
	}
	if bumpClone == nil {
		t.Fatal("hoisted bump clone not found")
	}
	// Walk the hoisted body looking for the `n` reference —
	// must be CaptureRef, not Ident.
	saw := false
	walkExpr(bumpClone.Body, func(e ast.Expr) {
		if id, ok := e.(*ast.Ident); ok && id.Name == "n" {
			t.Errorf("hoisted body still has *ast.Ident{Name:%q}; capture rewrite missed it", id.Name)
		}
		if cr, ok := e.(*ast.CaptureRef); ok && cr.Name == "n" {
			saw = true
		}
	})
	if !saw {
		t.Error("expected at least one *ast.CaptureRef{Name:\"n\"} in hoisted body")
	}
}

// TestConvertLambdaExpression — `var f = function (x): T { ... }`
// is a Lambda expression form; closureconv should hoist it to
// a top-level decl just like a named local function.
func TestConvertLambdaExpression(t *testing.T) {
	src := `function main(): i32 {
		var k: i32 = 7;
		var mul: (i32) => i32 = function (x: i32): i32 { return x * k; };
		return mul(6);
	}`
	prog := runConvert(t, src)
	// Same shape: hoisted lambda is a `__closure_lambda_<N>`
	// FuncDecl in prog.Funcs.
	var sawHoisted bool
	for _, fn := range prog.Funcs {
		if strings.HasPrefix(fn.Name, "__closure_lambda_") {
			sawHoisted = true
		}
	}
	if !sawHoisted {
		t.Error("hoisted clone of lambda not found in prog.Funcs")
	}
	// `mul`'s Init should be MakeClosure, with `k` listed in
	// Captures.
	mainFn := findFuncByName(prog, "main")
	if mainFn == nil {
		t.Fatal("main vanished")
	}
	mulDef := findVarStmt(mainFn.Body, "mul")
	if mulDef == nil {
		t.Fatal("def site `var mul = ...` not found")
	}
	mc, ok := mulDef.Init.(*ast.MakeClosure)
	if !ok {
		t.Fatalf("mul init: expected *ast.MakeClosure, got %T", mulDef.Init)
	}
	if len(mc.Captures) == 0 {
		t.Errorf("mul's MakeClosure has no Captures, but should have captured `k`")
	}
}

// TestConvertNoOpWhenNoLocalFunctions — a program with no
// IsLocal func / lambda should round-trip unchanged. Guards
// against the converter accidentally rewriting top-level
// decls.
func TestConvertNoOpWhenNoLocalFunctions(t *testing.T) {
	src := `function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return add(40, 2); }`
	before := mustParseCheck(t, src)
	beforeCount := len(before.Funcs)
	prog := runConvert(t, src)
	if len(prog.Funcs) != beforeCount {
		t.Errorf("no-op program grew from %d to %d funcs", beforeCount, len(prog.Funcs))
	}
	mainFn := findFuncByName(prog, "main")
	if mainFn == nil {
		t.Fatal("main went away")
	}
	// main's body should still be `return add(40, 2);` — no
	// MakeClosure or Var-with-MakeClosure should have been
	// introduced.
	walkExpr(mainFn.Body, func(e ast.Expr) {
		if _, ok := e.(*ast.MakeClosure); ok {
			t.Error("unexpected MakeClosure inserted into no-op program")
		}
		if _, ok := e.(*ast.CaptureRef); ok {
			t.Error("unexpected CaptureRef inserted into no-op program")
		}
	})
}

// ---- helpers ----

func mustParseCheck(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	return prog
}

func findFuncByName(prog *ast.Program, name string) *ast.FuncDecl {
	for _, fn := range prog.Funcs {
		if fn.Name == name {
			return fn
		}
	}
	return nil
}

// walkExpr recurses through every ast.Expr child of every
// statement in blk, calling visit on each. Conservative — only
// covers the shapes the test programs above produce.
func walkExpr(blk *ast.Block, visit func(ast.Expr)) {
	if blk == nil {
		return
	}
	for _, s := range blk.Stmts {
		walkStmtExpr(s, visit)
	}
}

func walkStmtExpr(s ast.Stmt, visit func(ast.Expr)) {
	switch x := s.(type) {
	case *ast.Var:
		walkInExpr(x.Init, visit)
	case *ast.ExprStmt:
		walkInExpr(x.Expr, visit)
	case *ast.Return:
		walkInExpr(x.Value, visit)
	case *ast.Block:
		walkExpr(x, visit)
	case *ast.If:
		walkInExpr(x.Cond, visit)
		walkStmtExpr(x.Then, visit)
		if x.Else != nil {
			walkStmtExpr(x.Else, visit)
		}
	}
}

func walkInExpr(e ast.Expr, visit func(ast.Expr)) {
	if e == nil {
		return
	}
	visit(e)
	switch x := e.(type) {
	case *ast.Binary:
		walkInExpr(x.Left, visit)
		walkInExpr(x.Right, visit)
	case *ast.Unary:
		walkInExpr(x.Operand, visit)
	case *ast.Call:
		walkInExpr(x.Callee, visit)
		for _, a := range x.Args {
			walkInExpr(a, visit)
		}
	}
}
