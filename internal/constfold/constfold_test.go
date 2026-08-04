package constfold

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// fold parses src, runs Fold, and returns the post-fold program.
// Tests then assert against the substituted shape (typically by
// finding a function and inspecting its return statement).
func fold(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := Fold(prog, nil); err != nil {
		t.Fatalf("fold: %v", err)
	}
	return prog
}

// foldErr expects Fold to fail and returns the error message.
func foldErr(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := Fold(prog, nil); err != nil {
		return err.Error()
	}
	t.Fatal("expected fold error but got none")
	return ""
}

// returnLit fishes the literal returned by `function main` so a
// test can compare against the resolved const value. The constfold
// pass should have replaced any Ident reference with a literal node
// before returning.
func returnLit(t *testing.T, prog *ast.Program) ast.Expr {
	t.Helper()
	for _, fn := range prog.Funcs {
		if fn.Name != "main" {
			continue
		}
		for _, st := range fn.Body.Stmts {
			if r, ok := st.(*ast.Return); ok {
				return r.Value
			}
		}
	}
	t.Fatal("main has no return statement")
	return nil
}

// A bare integer const folds and gets substituted as a NumberLit at
// every reference site. The decl itself is dropped from the program.
func TestFoldNumberLiteral(t *testing.T) {
	prog := fold(t, `const N: i32 = 42;
function main(): i32 { return N; }`)
	if len(prog.Consts) != 0 {
		t.Errorf("expected const decls to be stripped, got %v", prog.Consts)
	}
	lit, ok := returnLit(t, prog).(*ast.NumberLit)
	if !ok {
		t.Fatalf("return value should be NumberLit, got %T", returnLit(t, prog))
	}
	if lit.Value != 42 {
		t.Errorf("got %d, want 42", lit.Value)
	}
}

// Type annotations are optional — when omitted the value's natural
// type is used and the substitution still produces a literal of the
// matching kind.
func TestFoldInferredType(t *testing.T) {
	prog := fold(t, `const N = 7;
function main(): i32 { return N; }`)
	if _, ok := returnLit(t, prog).(*ast.NumberLit); !ok {
		t.Errorf("expected NumberLit substitution, got %T", returnLit(t, prog))
	}
}

// Const-expr arithmetic over earlier consts folds to a single
// literal. This is the second-tier ability we promised in the design
// — references to PI work because PI was declared and folded first.
func TestFoldArithmeticOverEarlierConst(t *testing.T) {
	prog := fold(t, `const PI: f32 = 3.14;
const TWO_PI: f32 = PI * 2.0;
function main(): f32 { return TWO_PI; }`)
	lit, ok := returnLit(t, prog).(*ast.FloatLit)
	if !ok {
		t.Fatalf("return value should be FloatLit, got %T", returnLit(t, prog))
	}
	if lit.Value != 6.28 {
		t.Errorf("got %v, want 6.28", lit.Value)
	}
}

// Boolean operators fold too: `&&` / `||` / `!` all reduce when the
// operands are constant.
func TestFoldBooleanLogic(t *testing.T) {
	prog := fold(t, `const A: boolean = true;
const B: boolean = false;
const C: boolean = A && !B;
function main(): boolean { return C; }`)
	lit, ok := returnLit(t, prog).(*ast.BoolLit)
	if !ok {
		t.Fatalf("return value should be BoolLit, got %T", returnLit(t, prog))
	}
	if !lit.Value {
		t.Errorf("got %v, want true", lit.Value)
	}
}

// String concatenation between constant strings folds. Bonus: the
// resulting StringLit pretty-prints back to its source-text shape.
func TestFoldStringConcat(t *testing.T) {
	prog := fold(t, `const HELLO: string = "hello";
const GREETING: string = HELLO + ", world";
function main(): string { return GREETING; }`)
	lit, ok := returnLit(t, prog).(*ast.StringLit)
	if !ok {
		t.Fatalf("return value should be StringLit, got %T", returnLit(t, prog))
	}
	if lit.Value != "hello, world" {
		t.Errorf("got %q, want %q", lit.Value, "hello, world")
	}
}

// Forward references are rejected — consts must reference earlier
// consts only. The error names the offending identifier so users
// can re-order their declarations.
func TestFoldRejectsForwardReference(t *testing.T) {
	got := foldErr(t, `const X: i32 = Y;
const Y: i32 = 5;`)
	if !strings.Contains(got, `"Y"`) {
		t.Errorf("error should mention `Y`; got %v", got)
	}
}

// A non-constant initialiser (function call, variable read, struct
// literal …) is rejected with an explanatory message rather than
// silently breaking later in the pipeline.
func TestFoldRejectsRuntimeExpression(t *testing.T) {
	got := foldErr(t, `function helper(): i32 { return 1; }
const X: i32 = helper();`)
	if !strings.Contains(got, "not a constant") {
		t.Errorf("error should explain non-constant; got %v", got)
	}
}

// Type mismatches between annotation and resolved value surface as
// fold-time errors so the user sees them at the const, not at
// every downstream usage site.
func TestFoldRejectsTypeMismatch(t *testing.T) {
	got := foldErr(t, `const X: f32 = 5;`)
	if !strings.Contains(got, "declared type") {
		t.Errorf("error should mention declared type; got %v", got)
	}
}

// Division and modulo by zero in a constant initialiser are caught
// here so the program never reaches codegen with the poison value.
func TestFoldRejectsConstantDivByZero(t *testing.T) {
	got := foldErr(t, `const X: i32 = 10 / 0;`)
	if !strings.Contains(got, "division by zero") {
		t.Errorf("error should mention division by zero; got %v", got)
	}
}

// The saturating operators (#5542) are rejected in a constant
// initialiser rather than folded: folding runs BEFORE the checker, so
// no operand width — and therefore no clamp bound — is known yet.
// Guessing one would silently pick i32's bounds for a u8 const.
func TestFoldRejectsSaturatingOperators(t *testing.T) {
	for _, src := range []string{
		`const X: i32 = 1 +| 2;`,
		`const X: i32 = 5 -| 2;`,
		`const X: i32 = 3 *| 4;`,
		`const X: i32 = 1 <<| 31;`,
	} {
		got := foldErr(t, src)
		if !strings.Contains(got, "not allowed in integer constant expressions") {
			t.Errorf("%s: error should reject the operator; got %v", src, got)
		}
	}
}

// Substitution reaches every expression position the language
// supports — array elements, ternary arms, struct fields,
// conditions inside loops and ifs. This guards against a regression
// where a new expression node skips the walk.
func TestFoldSubstitutesAcrossExpressionPositions(t *testing.T) {
	prog := fold(t, `const N: i32 = 3;
function main(): i32 {
	var arr: i32[] = [N, N + 1, N * 2];
	var k: i32 = arr[N - 1];
	if (k > N) { return k; }
	return 0;
}`)
	if c := countIdents(prog, "N"); c != 0 {
		t.Errorf("expected no remaining `N` Idents after substitution, found %d", c)
	}
}

// countIdents walks the entire post-fold program and tallies any
// Ident nodes whose name matches `target`. Used to confirm that
// substitution didn't leave a stray reference behind.
func countIdents(prog *ast.Program, target string) int {
	count := 0
	var walkExpr func(e ast.Expr)
	var walkStmt func(s ast.Stmt)
	walkExpr = func(e ast.Expr) {
		if e == nil {
			return
		}
		switch x := e.(type) {
		case *ast.Ident:
			if x.Name == target {
				count++
			}
		case *ast.Binary:
			walkExpr(x.Left)
			walkExpr(x.Right)
		case *ast.Unary:
			walkExpr(x.Operand)
		case *ast.Index:
			walkExpr(x.Array)
			walkExpr(x.Idx)
		case *ast.ArrayLit:
			for _, el := range x.Elems {
				walkExpr(el)
			}
		case *ast.Call:
			walkExpr(x.Callee)
			for _, a := range x.Args {
				walkExpr(a)
			}
		case *ast.IfExpr:
			walkExpr(x.Cond)
			walkExpr(x.Then)
			walkExpr(x.Else)
		case *ast.Assign:
			walkExpr(x.Target)
			walkExpr(x.Value)
		case *ast.StructLit:
			for _, fi := range x.Fields {
				walkExpr(fi.Value)
			}
		case *ast.FieldAccess:
			walkExpr(x.Target)
		// These mirror the compound forms the substituter walks. Without them
		// this helper cannot see a stray reference inside a cast / slice bound
		// / tuple element / lambda body — exactly the forms substitution used
		// to miss (#5477), so the gap hid itself from the tests.
		case *ast.CastExpr:
			walkExpr(x.Inner)
		case *ast.DowncastExpr:
			walkExpr(x.Inner)
		case *ast.SliceExpr:
			walkExpr(x.Source)
			walkExpr(x.Low)
			walkExpr(x.High)
		case *ast.TupleLit:
			for _, el := range x.Elems {
				walkExpr(el)
			}
		case *ast.MapLit:
			for _, en := range x.Entries {
				walkExpr(en.Key)
				walkExpr(en.Value)
			}
		case *ast.EnumLit:
			for _, a := range x.Args {
				walkExpr(a)
			}
		case *ast.FString:
			for _, p := range x.Parts {
				walkExpr(p.Expr)
			}
			walkExpr(x.Desugared)
		case *ast.Lambda:
			walkStmt(x.Body)
		}
	}
	walkStmt = func(s ast.Stmt) {
		if s == nil {
			return
		}
		switch x := s.(type) {
		case *ast.Block:
			for _, c := range x.Stmts {
				walkStmt(c)
			}
		case *ast.If:
			walkExpr(x.Cond)
			walkStmt(x.Then)
			walkStmt(x.Else)
		case *ast.While:
			walkExpr(x.Cond)
			walkStmt(x.Body)
		case *ast.For:
			walkStmt(x.Init)
			walkExpr(x.Cond)
			walkStmt(x.Step)
			walkStmt(x.Body)
		case *ast.Return:
			walkExpr(x.Value)
		case *ast.Var:
			walkExpr(x.Init)
		case *ast.ExprStmt:
			walkExpr(x.Expr)
		}
	}
	for _, fn := range prog.Funcs {
		walkStmt(fn.Body)
	}
	return count
}
