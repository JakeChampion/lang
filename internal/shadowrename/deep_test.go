package shadowrename_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// These tests deepen coverage of the shadow-renaming pass beyond
// the single-level cases in shadowrename_test.go. They confirm
// behaviour observed directly from the post-Rename AST:
//
//   - the rename counter is monotonic *per function*, so every
//     shadowed declaration in a function gets a globally-distinct
//     `name$N` suffix regardless of nesting depth or sibling
//     position;
//   - references inside a scope resolve to the innermost visible
//     (possibly renamed) declaration;
//   - shadowing works across blocks, loop bodies/inits, function
//     params, and match-arm payload bindings.

// findInner returns the Ident reached by following the path of
// nested *ast.Block stmts to the requested depth, then the named
// Var's init / a Return value. Kept deliberately small; the tests
// below pull out exactly the node they assert on.

// TestRenameNestedThreeLevelsEachGetsDistinctName — three blocks
// nested 3 deep each redeclare `x`. The outer keeps `x`; each
// inner level must pick up a distinct `x$N` suffix (the counter
// is per-function and monotonic), and the deepest reference must
// resolve to the deepest declaration.
func TestRenameNestedThreeLevelsEachGetsDistinctName(t *testing.T) {
	prog := runRename(t, `function f(): i32 {
		var x: i32 = 1;
		{
			var x: i32 = 2;
			{
				var x: i32 = 3;
				{
					var x: i32 = 4;
					return x;
				}
			}
		}
	}`)
	fn := prog.Funcs[0]
	names := collectVarNames(fn.Body)
	if len(names) != 4 {
		t.Fatalf("expected 4 var decls, got %d (%v)", len(names), names)
	}
	if names[0] != "x" {
		t.Errorf("outermost var: got %q, want %q", names[0], "x")
	}
	// Levels 1..3 must each be a distinct `x$N`.
	seen := map[string]bool{"x": true}
	for _, n := range names[1:] {
		if !strings.HasPrefix(n, "x$") {
			t.Errorf("shadowed var %q: want `x$<N>` form", n)
		}
		if seen[n] {
			t.Errorf("duplicate shadow name %q across nesting levels", n)
		}
		seen[n] = true
	}
	// The deepest return must reference the deepest decl name.
	deepest := names[len(names)-1]
	ret := lastReturnIdent(t, fn.Body)
	if ret != deepest {
		t.Errorf("deepest return references %q, want deepest decl %q", ret, deepest)
	}
}

// TestRenameNestedSiblingCounterStaysGlobal — three *sibling*
// blocks (not nested) each redeclare `x`. None is visible to the
// others, yet each is still given a unique suffix because the
// counter is per-function, never reset per scope. We assert all
// three suffixes are pairwise distinct (extends the existing
// sibling test, which only checks the first pair).
func TestRenameNestedSiblingCounterStaysGlobal(t *testing.T) {
	prog := runRename(t, `function f(): i32 {
		var x: i32 = 1;
		{ var x: i32 = 2; }
		{ var x: i32 = 3; }
		{ var x: i32 = 4; }
		return x;
	}`)
	fn := prog.Funcs[0]
	names := collectVarNames(fn.Body)
	if len(names) != 4 {
		t.Fatalf("expected 4 var decls, got %d (%v)", len(names), names)
	}
	if names[0] != "x" {
		t.Errorf("outer var: got %q, want %q", names[0], "x")
	}
	seen := map[string]bool{}
	for _, n := range names[1:] {
		if !strings.HasPrefix(n, "x$") {
			t.Errorf("sibling shadow %q: want `x$<N>` form", n)
		}
		if seen[n] {
			t.Errorf("two sibling shadows share name %q", n)
		}
		seen[n] = true
	}
}

// TestRenameSelfShadowInitReadsOuter — `var x = x + 10` inside an
// inner block: the RHS is evaluated in the *outer* scope (so it
// references the outer, un-suffixed `x`) while the LHS binds a
// fresh `x$N`. The subsequent reference resolves to the new name.
func TestRenameSelfShadowInitReadsOuter(t *testing.T) {
	prog := runRename(t, `function f(): i32 {
		var x: i32 = 1;
		{
			var x: i32 = x + 10;
			return x;
		}
	}`)
	fn := prog.Funcs[0]
	inner := firstBlock(t, fn.Body)
	var xVar *ast.Var
	for _, s := range inner.Stmts {
		if v, ok := s.(*ast.Var); ok {
			xVar = v
			break
		}
	}
	if xVar == nil {
		t.Fatal("inner x var not found")
	}
	if !strings.HasPrefix(xVar.Name, "x$") {
		t.Errorf("inner decl: got %q, want `x$<N>`", xVar.Name)
	}
	// RHS `x + 10` left operand must be the OUTER x (no suffix).
	bin, ok := xVar.Init.(*ast.Binary)
	if !ok {
		t.Fatalf("init: want Binary, got %T", xVar.Init)
	}
	id, ok := bin.Left.(*ast.Ident)
	if !ok {
		t.Fatalf("init.Left: want Ident, got %T", bin.Left)
	}
	if id.Name != "x" {
		t.Errorf("init RHS references %q, want outer %q", id.Name, "x")
	}
	// The return after the decl must reference the renamed inner x.
	ret := lastReturnIdent(t, inner)
	if ret != xVar.Name {
		t.Errorf("return references %q, want inner decl %q", ret, xVar.Name)
	}
}

// TestRenameLoopInitShadowsOuterVar — a for-loop init declares a
// var with the same name as an outer var. The init/cond/step/body
// all share the loop scope and must reference the renamed loop
// variable, while a reference after the loop resolves to the
// untouched outer var.
func TestRenameLoopInitShadowsOuterVar(t *testing.T) {
	prog := runRename(t, `function f(): i32 {
		var i: i32 = 100;
		var sum: i32 = 0;
		for (var i: i32 = 0; i < 3; i = i + 1) {
			sum = sum + i;
		}
		return sum + i;
	}`)
	fn := prog.Funcs[0]
	var forStmt *ast.For
	for _, s := range fn.Body.Stmts {
		if fs, ok := s.(*ast.For); ok {
			forStmt = fs
			break
		}
	}
	if forStmt == nil {
		t.Fatal("for stmt not found")
	}
	// Loop init var must be renamed (shadows outer `i`).
	initVar, ok := forStmt.Init.(*ast.Var)
	if !ok {
		t.Fatalf("for init: want Var, got %T", forStmt.Init)
	}
	loopName := initVar.Name
	if !strings.HasPrefix(loopName, "i$") {
		t.Errorf("loop var: got %q, want `i$<N>`", loopName)
	}
	// Cond `i < 3` references the loop var.
	cond, ok := forStmt.Cond.(*ast.Binary)
	if !ok {
		t.Fatalf("cond: want Binary, got %T", forStmt.Cond)
	}
	if id, ok := cond.Left.(*ast.Ident); !ok || id.Name != loopName {
		t.Errorf("cond left: got %v, want %q", cond.Left, loopName)
	}
	// Step `i = i + 1` references the loop var on both sides.
	stepExpr, ok := forStmt.Step.(*ast.ExprStmt)
	if !ok {
		t.Fatalf("step: want ExprStmt, got %T", forStmt.Step)
	}
	asn, ok := stepExpr.Expr.(*ast.Assign)
	if !ok {
		t.Fatalf("step expr: want Assign, got %T", stepExpr.Expr)
	}
	if id, ok := asn.Target.(*ast.Ident); !ok || id.Name != loopName {
		t.Errorf("step target: got %v, want %q", asn.Target, loopName)
	}
	// Body `sum = sum + i` references the loop var, not outer `i`.
	body, ok := forStmt.Body.(*ast.Block)
	if !ok {
		t.Fatalf("for body: want Block, got %T", forStmt.Body)
	}
	bodyIdents := collectIdentNames(body)
	if !contains(bodyIdents, loopName) {
		t.Errorf("loop body idents %v missing loop var %q", bodyIdents, loopName)
	}
	if contains(bodyIdents, "i") {
		t.Errorf("loop body references bare outer %q; should use %q", "i", loopName)
	}
	// `return sum + i` after the loop references the OUTER i.
	retBin := lastReturnBinary(t, fn.Body)
	if id, ok := retBin.Right.(*ast.Ident); !ok || id.Name != "i" {
		t.Errorf("post-loop return right: got %v, want outer %q", retBin.Right, "i")
	}
}

// TestRenameLocalShadowsParam — a function param `n` shadowed by a
// local `var n` inside a block. The local's init reads the param
// (outer scope), the local gets a fresh `n$N`, and the return
// inside the block resolves to the local. Confirms params
// participate in shadow detection (they're bound into the
// function's top frame).
func TestRenameLocalShadowsParam(t *testing.T) {
	prog := runRename(t, `function f(n: i32): i32 {
		{
			var n: i32 = n + 5;
			return n;
		}
	}`)
	fn := prog.Funcs[0]
	// Param name must be untouched.
	if fn.Params[0].Name != "n" {
		t.Errorf("param renamed to %q, want %q", fn.Params[0].Name, "n")
	}
	inner := firstBlock(t, fn.Body)
	var nVar *ast.Var
	for _, s := range inner.Stmts {
		if v, ok := s.(*ast.Var); ok {
			nVar = v
			break
		}
	}
	if nVar == nil {
		t.Fatal("local n not found")
	}
	if !strings.HasPrefix(nVar.Name, "n$") {
		t.Errorf("local shadowing param: got %q, want `n$<N>`", nVar.Name)
	}
	// init `n + 5` reads the param.
	bin, ok := nVar.Init.(*ast.Binary)
	if !ok {
		t.Fatalf("init: want Binary, got %T", nVar.Init)
	}
	if id, ok := bin.Left.(*ast.Ident); !ok || id.Name != "n" {
		t.Errorf("init reads %v, want param %q", bin.Left, "n")
	}
	// return resolves to the local.
	if got := lastReturnIdent(t, inner); got != nVar.Name {
		t.Errorf("return references %q, want local %q", got, nVar.Name)
	}
}

// TestRenameMatchArmBindingShadowsOuter — a match-arm payload
// binding (`Val(x)`) shadows an outer `var x`. The binding and the
// arm body's reference to it must be renamed; the outer reference
// after the match resolves to the original `x`.
func TestRenameMatchArmBindingShadowsOuter(t *testing.T) {
	prog := runRename(t, `enum Box { Val(i32) }
	function f(b: Box): i32 {
		var x: i32 = 1;
		match (b) {
			Val(x) => { return x; }
		}
		return x;
	}`)
	fn := prog.Funcs[len(prog.Funcs)-1]
	var m *ast.Match
	for _, s := range fn.Body.Stmts {
		if ms, ok := s.(*ast.Match); ok {
			m = ms
			break
		}
	}
	if m == nil {
		t.Fatal("match stmt not found")
	}
	if len(m.Arms) != 1 || len(m.Arms[0].Bindings) != 1 {
		t.Fatalf("unexpected arm shape: %+v", m.Arms)
	}
	armName := m.Arms[0].Bindings[0]
	if !strings.HasPrefix(armName, "x$") {
		t.Errorf("arm binding: got %q, want `x$<N>`", armName)
	}
	// Arm body `return x` must resolve to the arm binding.
	if got := lastReturnIdent(t, m.Arms[0].Body); got != armName {
		t.Errorf("arm body return references %q, want arm binding %q", got, armName)
	}
	// The post-match `return x` resolves to the outer un-suffixed x.
	if got := lastReturnIdent(t, fn.Body); got != "x" {
		t.Errorf("post-match return references %q, want outer %q", got, "x")
	}
}

// ---- local helpers (distinct names from shadowrename_test.go) ----

func firstBlock(t *testing.T, b *ast.Block) *ast.Block {
	t.Helper()
	for _, s := range b.Stmts {
		if blk, ok := s.(*ast.Block); ok {
			return blk
		}
	}
	t.Fatal("no nested block found")
	return nil
}

// lastReturnIdent finds the last Return whose value is a bare
// Ident, searching this block and nested blocks depth-first.
func lastReturnIdent(t *testing.T, b *ast.Block) string {
	t.Helper()
	var found string
	var ok bool
	var walk func(blk *ast.Block)
	walk = func(blk *ast.Block) {
		if blk == nil {
			return
		}
		for _, s := range blk.Stmts {
			switch n := s.(type) {
			case *ast.Return:
				if id, isID := n.Value.(*ast.Ident); isID {
					found, ok = id.Name, true
				}
			case *ast.Block:
				walk(n)
			}
		}
	}
	walk(b)
	if !ok {
		t.Fatal("no Return-of-Ident found")
	}
	return found
}

func lastReturnBinary(t *testing.T, b *ast.Block) *ast.Binary {
	t.Helper()
	for _, s := range b.Stmts {
		if r, ok := s.(*ast.Return); ok {
			if bin, isBin := r.Value.(*ast.Binary); isBin {
				return bin
			}
		}
	}
	t.Fatal("no Return-of-Binary found")
	return nil
}

// collectIdentNames gathers Ident names appearing in a block's
// statements (one level deep into common expr shapes), enough for
// the loop-body assertions.
func collectIdentNames(b *ast.Block) []string {
	var out []string
	var fromExpr func(e ast.Expr)
	fromExpr = func(e ast.Expr) {
		switch n := e.(type) {
		case *ast.Ident:
			out = append(out, n.Name)
		case *ast.Binary:
			fromExpr(n.Left)
			fromExpr(n.Right)
		case *ast.Assign:
			fromExpr(n.Target)
			fromExpr(n.Value)
		}
	}
	for _, s := range b.Stmts {
		switch n := s.(type) {
		case *ast.ExprStmt:
			fromExpr(n.Expr)
		case *ast.Var:
			fromExpr(n.Init)
		case *ast.Return:
			fromExpr(n.Value)
		}
	}
	return out
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
