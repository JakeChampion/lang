package constfold

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
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
	let arr: i32[] = [N, N + 1, N * 2];
	let k: i32 = arr[N - 1];
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
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == target {
			count++
		}
		return true
	})
	return count
}

// A const may hold an array or tuple literal of constant elements (#7987).
// Elements settle to the declared element type, an empty literal keeps its
// declared element type, and every reference gets its own copy of the tree.
func TestFoldCompositeConsts(t *testing.T) {
	prog := fold(t, `const A: i32 = 2;
const XS: i32[] = [A, A * 3];
const E: i32[] = [];
const W: i64[] = [5000000000];
const T: (i32, string) = (4, "abc");
function main(): i32 { let a: i32[] = XS; let b: i32[] = XS; let e: i32[] = E; let w: i64[] = W; let t: (i32, string) = T; return 0; }`)
	inits := map[string]ast.Expr{}
	var order []ast.Expr
	for _, st := range prog.Funcs[0].Body.Stmts {
		if v, ok := st.(*ast.Var); ok {
			inits[v.Name] = v.Init
			order = append(order, v.Init)
		}
	}
	a, ok := inits["a"].(*ast.ArrayLit)
	if !ok || len(a.Elems) != 2 {
		t.Fatalf("XS should substitute a 2-element ArrayLit, got %T", inits["a"])
	}
	if n, ok := a.Elems[1].(*ast.NumberLit); !ok || n.Value != 6 || n.Width != 32 {
		t.Errorf("XS[1] = %#v, want the folded i32 literal 6", a.Elems[1])
	}
	if b, _ := inits["b"].(*ast.ArrayLit); b == a || b == nil || b.Elems[0] == a.Elems[0] {
		t.Error("two references to XS share a tree; each substitution must be its own copy")
	}
	if e, ok := inits["e"].(*ast.ArrayLit); !ok || len(e.Elems) != 0 || !ast.Equal(e.ElemType, ast.NumberType{Width: 32, Signed: true}) {
		t.Errorf("E should be an empty ArrayLit typed i32[], got %#v", inits["e"])
	}
	if w, ok := inits["w"].(*ast.ArrayLit); !ok || w.Elems[0].(*ast.NumberLit).Width != 64 {
		t.Errorf("W's element should carry the declared i64 width, got %#v", inits["w"])
	}
	if tl, ok := inits["t"].(*ast.TupleLit); !ok || len(tl.Elems) != 2 {
		t.Errorf("T should substitute a 2-element TupleLit, got %T", inits["t"])
	}
}

// A const may hold a struct literal of constant fields, nested ones included
// (#6685); each reference gets its own copy of the tree.
func TestFoldStructConsts(t *testing.T) {
	prog := fold(t, `struct P { x: i32, y: i32 }
struct C { p: P, s: string }
const B: i32 = 40;
const O: P = P { x: 1, y: B + 1 };
const K: C = C { p: O, s: "k" };
function main(): i32 { let a: P = O; let b: P = O; let k: C = K; return 0; }`)
	inits := map[string]ast.Expr{}
	for _, st := range prog.Funcs[0].Body.Stmts {
		if v, ok := st.(*ast.Var); ok {
			inits[v.Name] = v.Init
		}
	}
	a, ok := inits["a"].(*ast.StructLit)
	if !ok || a.TypeName != "P" || len(a.Fields) != 2 {
		t.Fatalf("O should substitute a P literal, got %#v", inits["a"])
	}
	if n, ok := a.Fields[1].Value.(*ast.NumberLit); !ok || n.Value != 41 {
		t.Errorf("O.y = %#v, want the folded literal 41", a.Fields[1].Value)
	}
	if b, _ := inits["b"].(*ast.StructLit); b == a || b == nil || b.Fields[0].Value == a.Fields[0].Value {
		t.Error("two references to O share a tree; each substitution must be its own copy")
	}
	k, ok := inits["k"].(*ast.StructLit)
	if !ok || len(k.Fields) != 2 {
		t.Fatalf("K should substitute a C literal, got %#v", inits["k"])
	}
	if p, ok := k.Fields[0].Value.(*ast.StructLit); !ok || p.TypeName != "P" {
		t.Errorf("K.p should be O's P literal, got %#v", k.Fields[0].Value)
	}
}

func TestFoldCompositeConstRefusals(t *testing.T) {
	for src, want := range map[string]string{
		`const XS: i32[] = ["a"];`:                                                                    "declared type i32 does not match initialiser type string",
		"function f(): i32 { return 1; }\nconst XS: i32[] = [f()];":                                   "not a constant",
		"struct P { x: i32 }\nfunction f(): i32 { return 1; }\nconst C: P = P { x: f() };":            "not a constant",
		"struct P { x: i32, y: i32 }\nconst A: P = P { x: 1, y: 2 };\nconst C: P = P { ...A, x: 3 };": "not a constant",
		`const T: (i32, string) = (1, 2);`:                                                            "declared type string does not match initialiser type i32",
		`const XS: i32[] = [1]; const Y: i32 = XS + 1;`:                                               "operands aren't both numbers",
	} {
		if got := foldErr(t, src); !strings.Contains(got, want) {
			t.Errorf("%s\n  error %q, want it to contain %q", src, got, want)
		}
	}
}
