package parser

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

func TestEmptyFunction(t *testing.T) {
	prog, err := Parse("function f() {}")
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Funcs) != 1 || prog.Funcs[0].Name != "f" {
		t.Fatalf("unexpected program: %+v", prog)
	}
	if _, ok := prog.Funcs[0].ReturnType.(ast.VoidType); !ok {
		t.Errorf("default return type should be void, got %s", prog.Funcs[0].ReturnType)
	}
}

func TestPrecedence(t *testing.T) {
	// 1 + 2 * 3 should parse as 1 + (2 * 3)
	prog, err := Parse("function f(): number { return 1 + 2 * 3; }")
	if err != nil {
		t.Fatal(err)
	}
	ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	bin := ret.Value.(*ast.Binary)
	if bin.Op != "+" {
		t.Fatalf("outer op = %q, want +", bin.Op)
	}
	rhs, ok := bin.Right.(*ast.Binary)
	if !ok || rhs.Op != "*" {
		t.Fatalf("rhs = %v, want * binary", bin.Right)
	}
}

func TestArrayLitAndIndex(t *testing.T) {
	prog, err := Parse("function f(): number { var a: number[] = [1,2,3]; return a[1]; }")
	if err != nil {
		t.Fatal(err)
	}
	v := prog.Funcs[0].Body.Stmts[0].(*ast.Var)
	if _, ok := v.Init.(*ast.ArrayLit); !ok {
		t.Errorf("expected ArrayLit init, got %T", v.Init)
	}
	r := prog.Funcs[0].Body.Stmts[1].(*ast.Return)
	if _, ok := r.Value.(*ast.Index); !ok {
		t.Errorf("expected Index return, got %T", r.Value)
	}
}

func TestAssignToCallIsError(t *testing.T) {
	_, err := Parse("function f() { f() = 1; }")
	if err == nil {
		t.Fatal("expected parse error for assign-to-call")
	}
}

func TestIfElse(t *testing.T) {
	prog, err := Parse("function f(): number { if (true) { return 1; } else { return 2; } }")
	if err != nil {
		t.Fatal(err)
	}
	ifs := prog.Funcs[0].Body.Stmts[0].(*ast.If)
	if ifs.Else == nil {
		t.Fatal("expected else branch")
	}
}

// `for` produces a real For node (so `continue` can target the step).
func TestForProducesForNode(t *testing.T) {
	prog, err := Parse(`function f(): number {
		for (var i = 0; i < 3; i = i + 1) { i; }
		return 0;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	loop, ok := prog.Funcs[0].Body.Stmts[0].(*ast.For)
	if !ok {
		t.Fatalf("expected For, got %T", prog.Funcs[0].Body.Stmts[0])
	}
	if _, ok := loop.Init.(*ast.Var); !ok {
		t.Errorf("Init should be Var, got %T", loop.Init)
	}
	if loop.Step == nil {
		t.Errorf("Step should be set")
	}
}

// `for x in arr { body }` desugars to a Block containing a few
// synthetic `var` declarations and a For loop (not a While, so
// `continue` advances the index via the For's step slot rather
// than skipping it).
func TestForEachOverArrayDesugars(t *testing.T) {
	prog, err := Parse(`function f(): number {
		var sum: number = 0;
		for x in [1, 2, 3] {
			sum = sum + x;
		}
		return sum;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	body := prog.Funcs[0].Body.Stmts
	blk, ok := body[1].(*ast.Block)
	if !ok {
		t.Fatalf("foreach should desugar to Block, got %T", body[1])
	}
	if len(blk.Stmts) != 4 {
		t.Fatalf("expected 4 inner stmts (iter / len / idx / for), got %d", len(blk.Stmts))
	}
	loop, ok := blk.Stmts[3].(*ast.For)
	if !ok {
		t.Fatalf("last stmt should be a For loop (so continue hits the step), got %T", blk.Stmts[3])
	}
	if loop.Step == nil {
		t.Errorf("desugared For must carry a step so `continue` advances the index")
	}
}

// `for c in "hi"` works the same way — strings support `len()` and
// indexing, so the desugar applies identically.
func TestForEachOverStringDesugars(t *testing.T) {
	prog, err := Parse(`function f(): number {
		var sum: number = 0;
		for c in "abc" {
			sum = sum + c;
		}
		return sum;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	body := prog.Funcs[0].Body.Stmts
	if _, ok := body[1].(*ast.Block); !ok {
		t.Fatalf("foreach over string should desugar to Block, got %T", body[1])
	}
}

// Nested foreach loops use unique synthetic slot names so the inner
// loop can't shadow the outer's iterator / length / index.
func TestForEachNestedHasUniqueSlots(t *testing.T) {
	prog, err := Parse(`function f(): number {
		var s: number = 0;
		for a in [1, 2] {
			for b in [3, 4] {
				s = s + a + b;
			}
		}
		return s;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var walk func(s ast.Stmt)
	walk = func(s ast.Stmt) {
		switch x := s.(type) {
		case *ast.Block:
			for _, c := range x.Stmts {
				walk(c)
			}
		case *ast.For:
			walk(x.Body)
		case *ast.While:
			walk(x.Body)
		case *ast.Var:
			if strings.HasPrefix(x.Name, "__foreach_") {
				if seen[x.Name] {
					t.Errorf("duplicate synthetic name %q", x.Name)
				}
				seen[x.Name] = true
			}
		}
	}
	for _, s := range prog.Funcs[0].Body.Stmts {
		walk(s)
	}
	if len(seen) != 6 {
		t.Errorf("expected 6 synthetic names (3 per nested loop), got %d: %v", len(seen), seen)
	}
}

// `break` and `continue` inside a foreach body target the desugared
// while loop — same semantics as a hand-written index loop.
func TestForEachBreakContinue(t *testing.T) {
	prog, err := Parse(`function f(): number {
		var sum: number = 0;
		for x in [1, 2, 3, 4] {
			if (x == 2) { continue; }
			if (x == 4) { break; }
			sum = sum + x;
		}
		return sum;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := prog.Funcs[0].Body.Stmts[1].(*ast.Block); !ok {
		t.Errorf("foreach with break/continue should still desugar to Block")
	}
}

func TestForWithExprInit(t *testing.T) {
	prog, err := Parse(`function f(): number {
		var i = 0;
		for (i = 0; i < 3; i = i + 1) {}
		return 0;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	_ = prog
}

func TestForEmptyInitAndStep(t *testing.T) {
	// `for (; cond; ) body` — no init, no step.
	prog, err := Parse(`function f(): number {
		var i = 0;
		for (; i < 3 ;) { i = i + 1; }
		return i;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	_ = prog
}

func TestFunctionTypeAnnotation(t *testing.T) {
	prog, err := Parse(`function apply(f: (number, number) => number, a: number, b: number): number {
		return f(a, b);
	}`)
	if err != nil {
		t.Fatal(err)
	}
	param := prog.Funcs[0].Params[0]
	ft, ok := param.Type.(*ast.FuncType)
	if !ok {
		t.Fatalf("expected *FuncType, got %T", param.Type)
	}
	if len(ft.Params) != 2 || !ast.Equal(ft.Result, ast.NumberType{}) {
		t.Errorf("unexpected signature: %s", ft)
	}
}

func TestNullaryFunctionType(t *testing.T) {
	prog, err := Parse(`function call(f: () => number): number { return f(); }`)
	if err != nil {
		t.Fatal(err)
	}
	ft := prog.Funcs[0].Params[0].Type.(*ast.FuncType)
	if len(ft.Params) != 0 {
		t.Errorf("expected 0 params, got %d", len(ft.Params))
	}
}

// The parser should keep going after a per-statement error so a single
// run reports every problem, not just the first.
func TestRecoversAndReportsMultiplePerStatement(t *testing.T) {
	src := `function f(): number {
		var x = ;
		var y = 1 +;
		return 0;
	}`
	prog, err := Parse(src)
	if err == nil {
		t.Fatal("expected errors")
	}
	if strings.Count(err.Error(), "parse error") < 2 {
		t.Errorf("expected at least 2 parse errors, got:\n%s", err.Error())
	}
	if prog == nil || len(prog.Funcs) != 1 {
		t.Errorf("expected 1 partial function, got %v", prog)
	}
}

// A junk top-level declaration shouldn't stop later, valid functions
// from being parsed.
func TestRecoversAtTopLevel(t *testing.T) {
	src := `garbage tokens here;
		function good(): number { return 42; }`
	prog, err := Parse(src)
	if err == nil {
		t.Fatal("expected errors")
	}
	if prog == nil || len(prog.Funcs) != 1 || prog.Funcs[0].Name != "good" {
		t.Errorf("expected `good` to still be parsed, got %v", prog)
	}
}

func TestFloatLiteralAndType(t *testing.T) {
	prog, err := Parse(`function f(x: float): float { return x + 1.5; }`)
	if err != nil {
		t.Fatal(err)
	}
	fn := prog.Funcs[0]
	if _, ok := fn.ReturnType.(ast.FloatType); !ok {
		t.Errorf("return type = %T, want FloatType", fn.ReturnType)
	}
	if _, ok := fn.Params[0].Type.(ast.FloatType); !ok {
		t.Errorf("param type = %T, want FloatType", fn.Params[0].Type)
	}
	bin := fn.Body.Stmts[0].(*ast.Return).Value.(*ast.Binary)
	lit, ok := bin.Right.(*ast.FloatLit)
	if !ok {
		t.Fatalf("rhs = %T, want *FloatLit", bin.Right)
	}
	if lit.Value != 1.5 {
		t.Errorf("value = %v, want 1.5", lit.Value)
	}
}

func TestSwitchParsesBasic(t *testing.T) {
	prog, err := Parse(`function f(n: number): number {
		switch (n) {
			case 1, 2: return 10;
			case 3: return 30;
			default: return 0;
		}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	sw := prog.Funcs[0].Body.Stmts[0].(*ast.Switch)
	if len(sw.Cases) != 2 {
		t.Fatalf("got %d cases, want 2", len(sw.Cases))
	}
	if len(sw.Cases[0].Values) != 2 {
		t.Errorf("first case has %d values, want 2", len(sw.Cases[0].Values))
	}
	if sw.Default == nil {
		t.Errorf("default block missing")
	}
}

func TestSwitchRejectsDuplicateDefault(t *testing.T) {
	_, err := Parse(`function f(n: number): number {
		switch (n) { default: return 0; default: return 1; }
	}`)
	if err == nil {
		t.Error("expected error on duplicate default")
	}
}

func TestCompoundAssignDesugars(t *testing.T) {
	prog, err := Parse(`function f(): number { var x: number = 1; x += 2; return x; }`)
	if err != nil {
		t.Fatal(err)
	}
	stmt := prog.Funcs[0].Body.Stmts[1].(*ast.ExprStmt)
	a, ok := stmt.Expr.(*ast.Assign)
	if !ok {
		t.Fatalf("compound `+=` should desugar to *Assign, got %T", stmt.Expr)
	}
	bin, ok := a.Value.(*ast.Binary)
	if !ok || bin.Op != "+" {
		t.Fatalf("RHS should be `+` Binary, got %v", a.Value)
	}
	if id, ok := bin.Left.(*ast.Ident); !ok || id.Name != "x" {
		t.Errorf("desugared LHS should reuse the target `x`, got %v", bin.Left)
	}
}

func TestTernary(t *testing.T) {
	prog, err := Parse(`function f(b: boolean): number { return b ? 1 : 2; }`)
	if err != nil {
		t.Fatal(err)
	}
	ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	tern, ok := ret.Value.(*ast.Ternary)
	if !ok {
		t.Fatalf("expected *Ternary, got %T", ret.Value)
	}
	if _, ok := tern.Cond.(*ast.Ident); !ok {
		t.Errorf("cond should be Ident, got %T", tern.Cond)
	}
}

func TestTernaryRightAssociative(t *testing.T) {
	// `a ? b : c ? d : e` parses as `a ? b : (c ? d : e)`.
	prog, err := Parse(`function f(a: boolean, c: boolean): number {
		return a ? 1 : c ? 2 : 3;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	tern := prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value.(*ast.Ternary)
	if _, ok := tern.Else.(*ast.Ternary); !ok {
		t.Fatalf("else should be a nested Ternary, got %T", tern.Else)
	}
}

func TestStructDecl(t *testing.T) {
	prog, err := Parse(`struct Point { x: number, y: number }`)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Structs) != 1 {
		t.Fatalf("got %d structs, want 1", len(prog.Structs))
	}
	sd := prog.Structs[0]
	if sd.Name != "Point" || len(sd.Fields) != 2 {
		t.Errorf("unexpected struct: %+v", sd)
	}
}

func TestStructLitAndFieldAccess(t *testing.T) {
	prog, err := Parse(`struct P { x: number }
		function main(): number {
			var p: P = P { x: 5 };
			return p.x;
		}`)
	if err != nil {
		t.Fatal(err)
	}
	main := prog.Funcs[0]
	v := main.Body.Stmts[0].(*ast.Var)
	if _, ok := v.Init.(*ast.StructLit); !ok {
		t.Errorf("init should be StructLit, got %T", v.Init)
	}
	ret := main.Body.Stmts[1].(*ast.Return)
	if _, ok := ret.Value.(*ast.FieldAccess); !ok {
		t.Errorf("return should be FieldAccess, got %T", ret.Value)
	}
}

func TestMethodReceiverParsing(t *testing.T) {
	prog, err := Parse(`struct Point { x: number, y: number }
		function (p: Point) sum(): number { return p.x + p.y; }`)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Funcs) != 1 {
		t.Fatalf("got %d funcs, want 1", len(prog.Funcs))
	}
	fn := prog.Funcs[0]
	if fn.Receiver == nil {
		t.Fatal("expected receiver, got nil")
	}
	if fn.Receiver.Name != "p" {
		t.Errorf("receiver name = %q, want \"p\"", fn.Receiver.Name)
	}
	if st, ok := fn.Receiver.Type.(ast.StructType); !ok || st.Name != "Point" {
		t.Errorf("receiver type = %v, want Point", fn.Receiver.Type)
	}
	if fn.Name != "sum" {
		t.Errorf("name = %q, want \"sum\"", fn.Name)
	}
}

func TestRegularFunctionStillParses(t *testing.T) {
	// Make sure the receiver lookahead doesn't trigger on a normal
	// function whose first param is a struct.
	prog, err := Parse(`struct P { x: number }
		function describe(p: P): number { return p.x; }`)
	if err != nil {
		t.Fatal(err)
	}
	if prog.Funcs[0].Receiver != nil {
		t.Error("regular function got mistakenly parsed as a method")
	}
}

// `pub` flips the FuncDecl.Public / StructDecl.Public flags. The
// modload step inspects those when deciding whether a cross-module
// reference is allowed.
func TestPubKeywordSetsPublicFlag(t *testing.T) {
	prog, err := Parse(`pub function exposed(): number { return 1; }
function hidden(): number { return 2; }
pub struct PubPoint { x: number }
struct PrivPoint { x: number }`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, fn := range prog.Funcs {
		got[fn.Name] = fn.Public
	}
	if !got["exposed"] {
		t.Error("`pub function exposed` should set FuncDecl.Public")
	}
	if got["hidden"] {
		t.Error("`function hidden` should leave FuncDecl.Public false")
	}
	gotS := map[string]bool{}
	for _, sd := range prog.Structs {
		gotS[sd.Name] = sd.Public
	}
	if !gotS["PubPoint"] {
		t.Error("`pub struct PubPoint` should set StructDecl.Public")
	}
	if gotS["PrivPoint"] {
		t.Error("`struct PrivPoint` should leave StructDecl.Public false")
	}
}

// `pub` is only valid in front of `function`, `struct`, or `const`.
// `pub var` (or any other kind of decl) should be rejected with a
// clear message rather than silently swallowed.
func TestPubBeforeUnsupportedKindIsError(t *testing.T) {
	_, err := Parse(`pub var x: number = 1;`)
	if err == nil {
		t.Fatal("expected parse error for `pub var`")
	}
	if !strings.Contains(err.Error(), "pub") {
		t.Errorf("error should mention `pub`; got %v", err)
	}
}

// Top-level `const NAME[: T] = expr;` parses into a ConstDecl on
// the program. Type annotations and the `pub` prefix are both
// optional.
func TestConstDeclParses(t *testing.T) {
	prog, err := Parse(`const N: number = 42;
const M = 7;
pub const PI: float = 3.14;`)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Consts) != 3 {
		t.Fatalf("expected 3 const decls, got %d", len(prog.Consts))
	}
	if prog.Consts[0].Name != "N" || prog.Consts[0].Type == nil {
		t.Errorf("first const should be `N: number`; got %+v", prog.Consts[0])
	}
	if prog.Consts[1].Name != "M" || prog.Consts[1].Type != nil {
		t.Errorf("second const should be `M` (no type annotation); got %+v", prog.Consts[1])
	}
	if !prog.Consts[2].Public {
		t.Errorf("third const should be public; got %+v", prog.Consts[2])
	}
}

// `enum Foo { Bar, Baz(string) }` parses into a top-level
// EnumDecl with variants in declaration order. Payload-less and
// payload-carrying variants both round-trip correctly.
func TestEnumDeclParses(t *testing.T) {
	prog, err := Parse(`enum Status { Ok, Err(string) }
pub enum Direction { N, S, E, W }`)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Enums) != 2 {
		t.Fatalf("expected 2 enum decls, got %d", len(prog.Enums))
	}
	st := prog.Enums[0]
	if st.Name != "Status" || st.Public {
		t.Errorf("first enum should be private `Status`; got %+v", st)
	}
	if len(st.Variants) != 2 {
		t.Fatalf("Status should have 2 variants; got %d", len(st.Variants))
	}
	if st.Variants[0].Name != "Ok" || len(st.Variants[0].Payloads) != 0 {
		t.Errorf("Ok should be payload-less; got %+v", st.Variants[0])
	}
	if st.Variants[1].Name != "Err" || len(st.Variants[1].Payloads) != 1 {
		t.Errorf("Err should carry one payload; got %+v", st.Variants[1])
	}
	if !prog.Enums[1].Public {
		t.Errorf("Direction should be public; got %+v", prog.Enums[1])
	}
}

// `match (e) { Variant => { … }, _ => { … } }` parses into a
// Match stmt with an Arms slice mirroring the source order.
func TestMatchStmtParses(t *testing.T) {
	prog, err := Parse(`enum E { A, B(number) }
function f(): number {
	var e: E = A;
	match (e) {
		A => { return 1; },
		B(n) => { return n; }
	}
	return -1;
}`)
	if err != nil {
		t.Fatal(err)
	}
	fn := prog.Funcs[0]
	var m *ast.Match
	for _, s := range fn.Body.Stmts {
		if mm, ok := s.(*ast.Match); ok {
			m = mm
			break
		}
	}
	if m == nil {
		t.Fatal("match stmt not found in function body")
	}
	if len(m.Arms) != 2 {
		t.Fatalf("expected 2 arms; got %d", len(m.Arms))
	}
	if m.Arms[0].VariantName != "A" || len(m.Arms[0].Bindings) != 0 {
		t.Errorf("first arm should be `A =>`; got %+v", m.Arms[0])
	}
	if m.Arms[1].VariantName != "B" || len(m.Arms[1].Bindings) != 1 || m.Arms[1].Bindings[0] != "n" {
		t.Errorf("second arm should be `B(n) =>`; got %+v", m.Arms[1])
	}
}
