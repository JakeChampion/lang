package shadowrename_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/shadowrename"
)

// runRename pipes src through parse + check + shadowrename and
// returns the mutated program. Mirrors the production
// pipeline shape: shadowrename runs after the checker.
func runRename(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	shadowrename.Rename(prog, info)
	return prog
}

// TestRenameLeavesUnshadowedNamesAlone — the pass should be a
// no-op when no variable is shadowed. Every Var node keeps the
// name the user wrote.
func TestRenameLeavesUnshadowedNamesAlone(t *testing.T) {
	prog := runRename(t, `function f(): i32 {
		var a: i32 = 1;
		var b: i32 = 2;
		return a + b;
	}`)
	fn := prog.Funcs[0]
	names := collectVarNames(fn.Body)
	if want := []string{"a", "b"}; !equal(names, want) {
		t.Errorf("got %v, want %v", names, want)
	}
}

// TestRenameShadowedDeclarationGetsFreshName — when an inner
// block redeclares a name from an outer scope, the inner var
// should pick up a `$N` suffix so the downstream slot-by-name
// scheme can keep them distinct.
func TestRenameShadowedDeclarationGetsFreshName(t *testing.T) {
	prog := runRename(t, `function f(): i32 {
		var x: i32 = 1;
		{
			var x: i32 = 2;
			x = x + 1;
		}
		return x;
	}`)
	fn := prog.Funcs[0]
	names := collectVarNames(fn.Body)
	// Two declarations: outer "x" stays, inner shadowed one
	// must get a `$N` suffix.
	if len(names) != 2 {
		t.Fatalf("expected 2 var decls, got %d (%v)", len(names), names)
	}
	if names[0] != "x" {
		t.Errorf("outer var: got %q, want %q", names[0], "x")
	}
	if !strings.HasPrefix(names[1], "x$") {
		t.Errorf("inner shadowed var: got %q, want `x$<N>` form", names[1])
	}
}

// TestRenameReferenceFollowsShadowedDecl — once a Var is renamed,
// every Ident reference inside its scope must point at the new
// name. Without that, the IR build's flat name lookup picks
// the wrong slot.
func TestRenameReferenceFollowsShadowedDecl(t *testing.T) {
	prog := runRename(t, `function f(): i32 {
		var x: i32 = 1;
		{
			var x: i32 = 2;
			var y: i32 = x + 10;
			return y;
		}
		return x;
	}`)
	fn := prog.Funcs[0]
	// Find the inner Block. The inner `y = x + 10`'s
	// rhs must reference the shadowed name (with $N suffix),
	// not the outer `x`.
	var innerBlock *ast.Block
	walkStmts(fn.Body, func(s ast.Stmt) {
		if b, ok := s.(*ast.Block); ok {
			innerBlock = b
		}
	})
	if innerBlock == nil {
		t.Fatal("inner block not found")
	}
	var yInit ast.Expr
	for _, s := range innerBlock.Stmts {
		if v, ok := s.(*ast.Var); ok && strings.HasPrefix(v.Name, "y") {
			yInit = v.Init
			break
		}
	}
	if yInit == nil {
		t.Fatal("y var-init not found")
	}
	// Init shape: `x + 10` — a Binary, whose Left is the x
	// reference.
	bin, ok := yInit.(*ast.Binary)
	if !ok {
		t.Fatalf("y init: expected Binary, got %T", yInit)
	}
	id, ok := bin.Left.(*ast.Ident)
	if !ok {
		t.Fatalf("y init.Left: expected Ident, got %T", bin.Left)
	}
	if !strings.HasPrefix(id.Name, "x$") {
		t.Errorf("inner reference to x: got %q, want `x$<N>` form", id.Name)
	}
}

// TestRenameSiblingBlocksDontInterfere — two sibling blocks
// each redeclaring `x` produce distinct fresh names, not the
// same suffix.
func TestRenameSiblingBlocksDontInterfere(t *testing.T) {
	prog := runRename(t, `function f(): i32 {
		var x: i32 = 1;
		{
			var x: i32 = 2;
		}
		{
			var x: i32 = 3;
		}
		return x;
	}`)
	fn := prog.Funcs[0]
	names := collectVarNames(fn.Body)
	if len(names) != 3 {
		t.Fatalf("expected 3 var decls, got %d (%v)", len(names), names)
	}
	if names[0] != "x" {
		t.Errorf("outer var: got %q, want %q", names[0], "x")
	}
	if names[1] == names[2] {
		t.Errorf("sibling shadowed vars share a name: both %q", names[1])
	}
}

// ---- helpers ----

func collectVarNames(b *ast.Block) []string {
	var out []string
	walkStmts(b, func(s ast.Stmt) {
		if v, ok := s.(*ast.Var); ok {
			out = append(out, v.Name)
		}
	})
	return out
}

func walkStmts(b *ast.Block, fn func(ast.Stmt)) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		fn(s)
		switch x := s.(type) {
		case *ast.Block:
			walkStmts(x, fn)
		case *ast.If:
			if then, ok := x.Then.(*ast.Block); ok {
				walkStmts(then, fn)
			}
			if x.Else != nil {
				if el, ok := x.Else.(*ast.Block); ok {
					walkStmts(el, fn)
				}
			}
		case *ast.While:
			if body, ok := x.Body.(*ast.Block); ok {
				walkStmts(body, fn)
			}
		case *ast.For:
			if body, ok := x.Body.(*ast.Block); ok {
				walkStmts(body, fn)
			}
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
