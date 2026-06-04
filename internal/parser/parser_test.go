package parser

import (
	"context"
	"errors"
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

// A type-name keyword that's also a stdlib module basename
// (`string`, `i32`, …) parses as a module qualifier in expression
// position when followed by `.` — `string.repeat_char(...)`. Without
// this, std/string's free functions are unreachable under no-prelude.
func TestKeywordModuleQualifier(t *testing.T) {
	prog, err := Parse(`function f(): string { return string.repeat_char(120, 4); }`)
	if err != nil {
		t.Fatalf("string.repeat_char should parse: %v", err)
	}
	call, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value.(*ast.Call)
	if !ok {
		t.Fatalf("expected a Call, got %T", prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value)
	}
	fa, ok := call.Callee.(*ast.FieldAccess)
	if !ok {
		t.Fatalf("callee should be FieldAccess (qualified ref), got %T", call.Callee)
	}
	id, ok := fa.Target.(*ast.Ident)
	if !ok || id.Name != "string" {
		t.Errorf("qualifier should be Ident `string`, got %v", fa.Target)
	}
	if fa.Field != "repeat_char" {
		t.Errorf("field should be `repeat_char`, got %q", fa.Field)
	}
	// A bare type keyword NOT followed by `.` is still not an
	// expression (stays a type-only token).
	if _, err := Parse(`function f(): i32 { return string; }`); err == nil {
		t.Error("bare `string` in expression position should still be a parse error")
	}
}

func TestPrecedence(t *testing.T) {
	// 1 + 2 * 3 should parse as 1 + (2 * 3)
	prog, err := Parse("function f(): i32 { return 1 + 2 * 3; }")
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

// Bitwise (& | ^) bind LOOSER than the comparison family (== != < <=
// > >=) in Fern. Mirrors parser.go's parseLogicalAnd → parseBitOr
// → parseBitXor → parseBitAnd → parseEquality → parseRelational
// hierarchy. The printer encodes the same ordering (see
// TestFormatBitwiseLooserThanCompare); if the two ever diverge,
// `(n & (n - 1)) == 0` round-trips through `n & n - 1 == 0` and
// re-parses as `n & ((n - 1) == 0)` — a different AST.
func TestBitwiseLooserThanCompare(t *testing.T) {
	// `a & b == c` parses as `a & (b == c)` because `==` binds
	// tighter than `&`. Outer is `&`.
	prog, err := Parse("function f(a: i32, b: i32, c: i32): boolean { return a & b == c; }")
	if err != nil {
		t.Fatal(err)
	}
	ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	bin, ok := ret.Value.(*ast.Binary)
	if !ok {
		t.Fatalf("return value = %T, want *ast.Binary", ret.Value)
	}
	if bin.Op != "&" {
		t.Fatalf("outer op = %q, want & (== should bind tighter)", bin.Op)
	}
	rhs, ok := bin.Right.(*ast.Binary)
	if !ok || rhs.Op != "==" {
		t.Fatalf("rhs = %v, want == binary", bin.Right)
	}

	// `(a & b) == c` parses as `(a & b) == c`. Outer is `==`.
	prog, err = Parse("function f(a: i32, b: i32, c: i32): boolean { return (a & b) == c; }")
	if err != nil {
		t.Fatal(err)
	}
	ret = prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	bin, ok = ret.Value.(*ast.Binary)
	if !ok {
		t.Fatalf("return value = %T, want *ast.Binary", ret.Value)
	}
	if bin.Op != "==" {
		t.Fatalf("outer op = %q, want == (parens force bitwise-first)", bin.Op)
	}
	lhs, ok := bin.Left.(*ast.Binary)
	if !ok || lhs.Op != "&" {
		t.Fatalf("lhs = %v, want & binary", bin.Left)
	}

	// `a & b | c` parses as `(a & b) | c` because `&` binds tighter
	// than `|`. Outer is `|`. Mirrors the parser hierarchy
	// parseBitOr → parseBitXor → parseBitAnd.
	prog, err = Parse("function f(a: i32, b: i32, c: i32): i32 { return a & b | c; }")
	if err != nil {
		t.Fatal(err)
	}
	ret = prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	bin, ok = ret.Value.(*ast.Binary)
	if !ok {
		t.Fatalf("return value = %T, want *ast.Binary", ret.Value)
	}
	if bin.Op != "|" {
		t.Fatalf("outer op = %q, want | (& should bind tighter)", bin.Op)
	}
	lhs, ok = bin.Left.(*ast.Binary)
	if !ok || lhs.Op != "&" {
		t.Fatalf("lhs = %v, want & binary", bin.Left)
	}
}

func TestArrayLitAndIndex(t *testing.T) {
	prog, err := Parse("function f(): i32 { var a: i32[] = [1,2,3]; return a[1]; }")
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
	prog, err := Parse("function f(): i32 { if (true) { return 1; } else { return 2; } }")
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
	prog, err := Parse(`function f(): i32 {
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
	prog, err := Parse(`function f(): i32 {
		var sum: i32 = 0;
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
	prog, err := Parse(`function f(): i32 {
		var sum: i32 = 0;
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
	prog, err := Parse(`function f(): i32 {
		var s: i32 = 0;
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

// `for (k, v) in m { ... }` desugars to an iterator-cursor loop:
// outer Block scopes a synthetic `__foreach_iter_N` bound to
// `expr.iter()`, the inner For drives `has_next()` / `advance()`
// and the body opens with `var k = it.key(); var v = it.value();`
// so the user's tuple names are bound before their stmts run.
func TestForEachOverMapTupleDesugars(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		var m: Map[i32, i32] = map_new(4);
		var sum: i32 = 0;
		for (k, v) in m {
			sum = sum + k + v;
		}
		return sum;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	body := prog.Funcs[0].Body.Stmts
	blk, ok := body[2].(*ast.Block)
	if !ok {
		t.Fatalf("for (k,v) in m should desugar to Block, got %T", body[2])
	}
	if len(blk.Stmts) != 2 {
		t.Fatalf("expected 2 inner stmts (iter / for), got %d", len(blk.Stmts))
	}
	declIter, ok := blk.Stmts[0].(*ast.Var)
	if !ok || !strings.HasPrefix(declIter.Name, "__foreach_iter_") {
		t.Fatalf("first stmt should declare __foreach_iter_N, got %T %#v", blk.Stmts[0], blk.Stmts[0])
	}
	loop, ok := blk.Stmts[1].(*ast.For)
	if !ok {
		t.Fatalf("second stmt should be For, got %T", blk.Stmts[1])
	}
	if loop.Step == nil {
		t.Errorf("desugared For must carry a step (advance) so `continue` advances the cursor")
	}
	innerBlk, ok := loop.Body.(*ast.Block)
	if !ok || len(innerBlk.Stmts) < 3 {
		t.Fatalf("loop body should open with two Var binds (k,v) before user stmts, got %T %d stmts", loop.Body, len(innerBlk.Stmts))
	}
	bindK, ok1 := innerBlk.Stmts[0].(*ast.Var)
	bindV, ok2 := innerBlk.Stmts[1].(*ast.Var)
	if !ok1 || bindK.Name != "k" {
		t.Errorf("first inner stmt should be `var k = ...`, got %#v", innerBlk.Stmts[0])
	}
	if !ok2 || bindV.Name != "v" {
		t.Errorf("second inner stmt should be `var v = ...`, got %#v", innerBlk.Stmts[1])
	}
}

// `break` and `continue` inside a foreach body target the desugared
// while loop — same semantics as a hand-written index loop.
func TestForEachBreakContinue(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		var sum: i32 = 0;
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
	prog, err := Parse(`function f(): i32 {
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
	prog, err := Parse(`function f(): i32 {
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
	prog, err := Parse(`function apply(f: (i32, i32) => i32, a: i32, b: i32): i32 {
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
	prog, err := Parse(`function call(f: () => i32): i32 { return f(); }`)
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
	src := `function f(): i32 {
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
		function good(): i32 { return 42; }`
	prog, err := Parse(src)
	if err == nil {
		t.Fatal("expected errors")
	}
	if prog == nil || len(prog.Funcs) != 1 || prog.Funcs[0].Name != "good" {
		t.Errorf("expected `good` to still be parsed, got %v", prog)
	}
}

func TestFloatLiteralAndType(t *testing.T) {
	prog, err := Parse(`function f(x: f32): f32 { return x + 1.5; }`)
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
	prog, err := Parse(`function f(n: i32): i32 {
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
	_, err := Parse(`function f(n: i32): i32 {
		switch (n) { default: return 0; default: return 1; }
	}`)
	if err == nil {
		t.Error("expected error on duplicate default")
	}
}

func TestCompoundAssignDesugars(t *testing.T) {
	prog, err := Parse(`function f(): i32 { var x: i32 = 1; x += 2; return x; }`)
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

// Compound assignment to a struct field (`a.v += 2`) must desugar the
// same way as a plain `=` lvalue — into `a.v = a.v + 2`. Previously the
// compound path only allowed Ident/Index targets and rejected a field
// lvalue with P003, even though plain `=` accepted it.
func TestCompoundAssignFieldDesugars(t *testing.T) {
	prog, err := Parse(`struct A { v: i32 } function f(): i32 { var a: A = A { v: 1 }; a.v += 2; return a.v; }`)
	if err != nil {
		t.Fatal(err)
	}
	stmt := prog.Funcs[0].Body.Stmts[1].(*ast.ExprStmt)
	a, ok := stmt.Expr.(*ast.Assign)
	if !ok {
		t.Fatalf("compound `+=` on a field should desugar to *Assign, got %T", stmt.Expr)
	}
	if _, ok := a.Target.(*ast.FieldAccess); !ok {
		t.Fatalf("assign target should be FieldAccess, got %T", a.Target)
	}
	bin, ok := a.Value.(*ast.Binary)
	if !ok || bin.Op != "+" {
		t.Fatalf("RHS should be `+` Binary, got %v", a.Value)
	}
	if _, ok := bin.Left.(*ast.FieldAccess); !ok {
		t.Errorf("desugared LHS should reuse the field target `a.v`, got %T", bin.Left)
	}
}

func TestIfExpr(t *testing.T) {
	prog, err := Parse(`function f(b: boolean): i32 { return if (b) { 1 } else { 2 }; }`)
	if err != nil {
		t.Fatal(err)
	}
	ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	ie, ok := ret.Value.(*ast.IfExpr)
	if !ok {
		t.Fatalf("expected *IfExpr, got %T", ret.Value)
	}
	if _, ok := ie.Cond.(*ast.Ident); !ok {
		t.Errorf("cond should be Ident, got %T", ie.Cond)
	}
}

// Postfix `?` parses as a TryOp node attached to the preceding
// primary. It binds tighter than any binary operator because the
// parseCall postfix loop runs before the precedence chain —
// `m.get(k)? + 1` parses as `(m.get(k)?) + 1`. The same node
// covers both Option and Result sources; the checker fills in
// Kind, so at parse time we just verify the shape.
func TestTryOpParses(t *testing.T) {
	prog, err := Parse(`function f(m: Map[i32, i32]): Option[i32] {
		var v: i32 = m.get(7)?;
		return Some(v + 1);
	}`)
	if err != nil {
		t.Fatal(err)
	}
	declStmt := prog.Funcs[0].Body.Stmts[0].(*ast.Var)
	tr, ok := declStmt.Init.(*ast.TryOp)
	if !ok {
		t.Fatalf("expected *TryOp as Var init, got %T", declStmt.Init)
	}
	if _, ok := tr.Inner.(*ast.Call); !ok {
		t.Errorf("inner of `?` should be the call `m.get(7)`, got %T", tr.Inner)
	}
}

// Hex integer literals parse to the right decimal value (base 16),
// independent of decimal-literal handling.
func TestHexLiteralValue(t *testing.T) {
	cases := map[string]int64{
		"0x2A":       42,
		"0xff":       255,
		"0X10":       16,
		"0x0":        0,
		"0xDEADBEEF": 3735928559,
	}
	for src, wantVal := range cases {
		prog, err := Parse(`function f(): i32 { return ` + src + `; }`)
		if err != nil {
			t.Errorf("%s: parse: %v", src, err)
			continue
		}
		ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
		lit, ok := ret.Value.(*ast.NumberLit)
		if !ok {
			t.Errorf("%s: expected *NumberLit, got %T", src, ret.Value)
			continue
		}
		if lit.Value != wantVal {
			t.Errorf("%s: Value = %d, want %d", src, lit.Value, wantVal)
		}
	}
}

// Typed numeric literal suffixes: lexer captures the suffix, the
// parser stamps Width / IsUnsigned at parse time so the checker
// sees a non-polymorphic type from the get-go.
func TestNumericLiteralSuffixes(t *testing.T) {
	type want struct {
		isFloat    bool
		width      int
		isUnsigned bool
	}
	cases := map[string]want{
		"42i8":   {false, 8, false},
		"42i16":  {false, 16, false},
		"42i32":  {false, 32, false},
		"42i64":  {false, 64, false},
		"42u8":   {false, 8, true},
		"42u32":  {false, 32, true},
		"42u64":  {false, 64, true},
		"1.5f32": {true, 32, false},
		"1.5f64": {true, 64, false},
		// Integer-shaped text + float suffix promotes to float.
		"42f64": {true, 64, false},
	}
	for src, w := range cases {
		prog, err := Parse(`function f(): i32 { return ` + src + `; }`)
		if err != nil {
			t.Errorf("%s: parse: %v", src, err)
			continue
		}
		ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
		if w.isFloat {
			lit, ok := ret.Value.(*ast.FloatLit)
			if !ok {
				t.Errorf("%s: expected *FloatLit, got %T", src, ret.Value)
				continue
			}
			if lit.Width != w.width {
				t.Errorf("%s: Width = %d, want %d", src, lit.Width, w.width)
			}
		} else {
			lit, ok := ret.Value.(*ast.NumberLit)
			if !ok {
				t.Errorf("%s: expected *NumberLit, got %T", src, ret.Value)
				continue
			}
			if lit.Width != w.width {
				t.Errorf("%s: Width = %d, want %d", src, lit.Width, w.width)
			}
			if lit.IsUnsigned != w.isUnsigned {
				t.Errorf("%s: IsUnsigned = %v, want %v", src, lit.IsUnsigned, w.isUnsigned)
			}
		}
	}
}

func TestNumericLiteralSuffixRejectsUnknown(t *testing.T) {
	if _, err := Parse(`function f(): i32 { return 42i33; }`); err == nil {
		t.Error("expected error for unknown numeric suffix")
	}
	if _, err := Parse(`function f(): i32 { return 1.5i32; }`); err == nil {
		t.Error("expected error: integer suffix on float literal")
	}
}

func TestIfExprNested(t *testing.T) {
	// `if (a) { 1 } else { if (c) { 2 } else { 3 } }` — the
	// nested IfExpr lives in the outer's Else slot. This is the
	// shape that used to round-trip as `a ? 1 : c ? 2 : 3`.
	prog, err := Parse(`function f(a: boolean, c: boolean): i32 {
		return if (a) { 1 } else { if (c) { 2 } else { 3 } };
	}`)
	if err != nil {
		t.Fatal(err)
	}
	ie := prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value.(*ast.IfExpr)
	if _, ok := ie.Else.(*ast.IfExpr); !ok {
		t.Fatalf("else should be a nested IfExpr, got %T", ie.Else)
	}
}

// `match` in expression position: each arm body is a single
// expression (no block). The parser routes to parseMatchExpr from
// parsePrimary's keyword switch, mirroring how `if` flips between
// statement and expression forms based on the dispatcher.
func TestMatchExprParses(t *testing.T) {
	prog, err := Parse(`function f(o: Option[i32]): i32 {
		return match (o) {
			Some(x) => x + 1,
			None    => 0
		};
	}`)
	if err != nil {
		t.Fatal(err)
	}
	ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	me, ok := ret.Value.(*ast.MatchExpr)
	if !ok {
		t.Fatalf("expected *MatchExpr, got %T", ret.Value)
	}
	if len(me.Arms) != 2 {
		t.Fatalf("expected 2 arms, got %d", len(me.Arms))
	}
	if me.Arms[0].VariantName != "Some" || len(me.Arms[0].Bindings) != 1 || me.Arms[0].Bindings[0] != "x" {
		t.Errorf("arm 0: got %+v, want Some(x)", me.Arms[0])
	}
	if me.Arms[1].VariantName != "None" || len(me.Arms[1].Bindings) != 0 {
		t.Errorf("arm 1: got %+v, want None", me.Arms[1])
	}
}

func TestStructDecl(t *testing.T) {
	prog, err := Parse(`struct Point { x: i32, y: i32 }`)
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
	prog, err := Parse(`struct P { x: i32 }
		function main(): i32 {
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

// A struct-update literal `P { ...base, field: v }` parses to a
// StructLit whose Base is the spread source and whose Fields hold
// only the overrides.
func TestStructUpdateLitParses(t *testing.T) {
	prog, err := Parse(`struct P { x: i32, y: i32 }
		function main(): i32 {
			var a: P = P { x: 1, y: 2 };
			var b: P = P { ...a, y: 9 };
			return b.y;
		}`)
	if err != nil {
		t.Fatal(err)
	}
	b := prog.Funcs[0].Body.Stmts[1].(*ast.Var)
	sl, ok := b.Init.(*ast.StructLit)
	if !ok {
		t.Fatalf("init should be StructLit, got %T", b.Init)
	}
	if sl.Base == nil {
		t.Fatal("struct-update literal should have a non-nil Base")
	}
	if id, ok := sl.Base.(*ast.Ident); !ok || id.Name != "a" {
		t.Errorf("Base should be Ident `a`, got %T %v", sl.Base, sl.Base)
	}
	if len(sl.Fields) != 1 || sl.Fields[0].Name != "y" {
		t.Errorf("overrides should be just [y], got %v", sl.Fields)
	}
}

// `P { ...base }` with no overrides is a legal pure copy: Base set,
// zero override fields.
func TestStructUpdateLitPureCopyParses(t *testing.T) {
	prog, err := Parse(`struct P { x: i32, y: i32 }
		function main(): i32 {
			var a: P = P { x: 1, y: 2 };
			var b: P = P { ...a };
			return b.x;
		}`)
	if err != nil {
		t.Fatal(err)
	}
	b := prog.Funcs[0].Body.Stmts[1].(*ast.Var)
	sl, ok := b.Init.(*ast.StructLit)
	if !ok {
		t.Fatalf("init should be StructLit, got %T", b.Init)
	}
	if sl.Base == nil {
		t.Fatal("pure-copy struct-update should have a non-nil Base")
	}
	if len(sl.Fields) != 0 {
		t.Errorf("pure copy should have no override fields, got %v", sl.Fields)
	}
}

func TestMethodReceiverParsing(t *testing.T) {
	prog, err := Parse(`struct Point { x: i32, y: i32 }
		function (p: Point) sum(): i32 { return p.x + p.y; }`)
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
	prog, err := Parse(`struct P { x: i32 }
		function describe(p: P): i32 { return p.x; }`)
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
	prog, err := Parse(`pub function exposed(): i32 { return 1; }
function hidden(): i32 { return 2; }
pub struct PubPoint { x: i32 }
struct PrivPoint { x: i32 }`)
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
	_, err := Parse(`pub var x: i32 = 1;`)
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
	prog, err := Parse(`const N: i32 = 42;
const M = 7;
pub const PI: f32 = 3.14;`)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Consts) != 3 {
		t.Fatalf("expected 3 const decls, got %d", len(prog.Consts))
	}
	if prog.Consts[0].Name != "N" || prog.Consts[0].Type == nil {
		t.Errorf("first const should be `N: i32`; got %+v", prog.Consts[0])
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
	prog, err := Parse(`enum E { A, B(i32) }
function f(): i32 {
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

// Generic enum decls carry positional type parameters in
// brackets after the name. Variant payload types may reference
// those parameters as bare identifiers; the parser preserves
// them as `StructType{Name: T}` and the checker rewrites them
// to ParamType after registration.
func TestGenericEnumDeclParses(t *testing.T) {
	prog, err := Parse(`enum Option[T] { Some(T), None }
enum Result[T, E] { Ok(T), Err(E) }`)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Enums) != 2 {
		t.Fatalf("expected 2 enum decls, got %d", len(prog.Enums))
	}
	if got := prog.Enums[0].TypeParams; len(got) != 1 || got[0] != "T" {
		t.Errorf("Option type params should be [T]; got %v", got)
	}
	if got := prog.Enums[1].TypeParams; len(got) != 2 || got[0] != "T" || got[1] != "E" {
		t.Errorf("Result type params should be [T, E]; got %v", got)
	}
}

// Generic instantiations parse into `EnumType{Name, Args}` at
// any type position. `Foo[T][]` (array of generic) keeps the
// brackets in the right order: generic instantiation first, the
// array suffix wraps the result.
func TestGenericInstantiationAtTypePosition(t *testing.T) {
	prog, err := Parse(`enum Option[T] { Some(T), None }
function f(): Option[i32] { return None; }
function g(xs: Option[string][]): i32 { return 0; }`)
	if err != nil {
		t.Fatal(err)
	}
	f := prog.Funcs[0]
	et, ok := f.ReturnType.(ast.EnumType)
	if !ok || et.Name != "Option" || len(et.Args) != 1 {
		t.Errorf("f's return type should be Option[i32]; got %+v", f.ReturnType)
	}
	g := prog.Funcs[1]
	at, ok := g.Params[0].Type.(ast.ArrayType)
	if !ok {
		t.Fatalf("g's first param should be an array; got %+v", g.Params[0].Type)
	}
	innerET, ok := at.Elem.(ast.EnumType)
	if !ok || innerET.Name != "Option" || len(innerET.Args) != 1 {
		t.Errorf("g's array element should be Option[string]; got %+v", at.Elem)
	}
}

// `let (a, b) = expr;` parses to a *ast.Destructure carrying
// the identifier list and the source expression — distinct
// from `let Variant(x) = …` which routes to *ast.LetElse.
func TestTupleDestructureParses(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		let (a, b) = (1, 2);
		return a + b;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	stmts := prog.Funcs[0].Body.Stmts
	d, ok := stmts[0].(*ast.Destructure)
	if !ok {
		t.Fatalf("first stmt should be *ast.Destructure; got %T", stmts[0])
	}
	if len(d.Names) != 2 || d.Names[0] != "a" || d.Names[1] != "b" {
		t.Errorf("names = %v, want [a b]", d.Names)
	}
	if _, ok := d.Init.(*ast.TupleLit); !ok {
		t.Errorf("Init should be a TupleLit; got %T", d.Init)
	}
}

// Tuple destructure requires at least 2 names — the language
// already reserves 1-element tuples (no singleton tuples), so
// `let (a) = …;` is treated as a parse error rather than a
// silently-degenerate binding.
func TestTupleDestructureSingleNameError(t *testing.T) {
	_, err := Parse(`function f(): i32 { let (a) = (1, 2); return a; }`)
	if err == nil {
		t.Fatal("expected parse error for single-name destructure")
	}
}

// `let Variant(b) = …` continues to route to LetElse — the
// tuple-destructure branch must not steal it. Smoke test.
func TestLetVariantStillParses(t *testing.T) {
	prog, err := Parse(`enum Option[T] { Some(T), None }
function f(): i32 {
	let Some(x) = Some(1) else { return 0; };
	return x;
}`)
	if err != nil {
		t.Fatal(err)
	}
	stmts := prog.Funcs[0].Body.Stmts
	if _, ok := stmts[0].(*ast.LetElse); !ok {
		t.Errorf("first stmt should still be *ast.LetElse; got %T", stmts[0])
	}
}

// TestParseContextCancellationShortCircuits checks that
// ParseContext returns ctx.Err() promptly when the context
// is cancelled mid-parse — the LSP rests on this so a fast
// typist's keystrokes don't pile up un-cancellable in-flight
// parses. The check runs once before parseProgram returns
// AND at each top-level decl boundary, so a pre-cancelled
// context aborts before parseProgram does any work.
func TestParseContextCancellationShortCircuits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: any ParseContext call should short-circuit.
	prog, err := ParseContext(ctx, `function f(): i32 { return 1; }
function g(): i32 { return 2; }
function h(): i32 { return 3; }`)
	if err == nil {
		t.Fatalf("expected ctx.Err(), got nil; prog has %d funcs", len(prog.Funcs))
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestParseContextHonoursBackgroundLikeOldParse — the
// non-cancellable case must behave exactly like the original
// `Parse(src)` API. Regression sentinel against accidentally
// breaking the wrapper.
func TestParseContextHonoursBackgroundLikeOldParse(t *testing.T) {
	prog, err := ParseContext(context.Background(), "function f(): i32 { return 42; }")
	if err != nil {
		t.Fatalf("ParseContext: %v", err)
	}
	if len(prog.Funcs) != 1 || prog.Funcs[0].Name != "f" {
		t.Errorf("got %+v, want single func named f", prog.Funcs)
	}
}

// Import aliases: `import "path" as name;` binds the qualifier to
// `name` (recorded in both LocalName and Alias); a plain import leaves
// Alias empty and derives LocalName from the path basename.
func TestImportAlias(t *testing.T) {
	prog, err := Parse(`import "std/test" as t;
import "std/string";
function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(prog.Imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(prog.Imports))
	}
	aliased := prog.Imports[0]
	if aliased.Path != "std/test" || aliased.LocalName != "t" || aliased.Alias != "t" {
		t.Errorf("aliased import = %+v, want Path=std/test LocalName=t Alias=t", aliased)
	}
	plain := prog.Imports[1]
	if plain.Path != "std/string" || plain.LocalName != "string" || plain.Alias != "" {
		t.Errorf("plain import = %+v, want Path=std/string LocalName=string Alias=\"\"", plain)
	}
	// `as` with no name is a parse error.
	if _, err := Parse(`import "std/test" as;`); err == nil {
		t.Error("`import ... as;` with no alias name should be a parse error")
	}
}

// `arena` is no longer a reserved word. The `arena { … }` block
// construct and the arena_save / arena_restore builtins it
// desugared to were all removed; per-request memory is now
// reclaimed by reference counting. So bare `arena` parses as an
// ordinary identifier.
func TestArenaIsNotReserved(t *testing.T) {
	prog, err := Parse("function f(): i32 { var arena: i32 = 5; return arena; }")
	if err != nil {
		t.Fatalf("`arena` should parse as an ordinary identifier: %v", err)
	}
	v, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Var)
	if !ok || v.Name != "arena" {
		t.Fatalf("expected `var arena`, got %T", prog.Funcs[0].Body.Stmts[0])
	}
}

// A `trait` declaration parses into Program.Traits with one
// TraitMethod per signature; the `self: Self` first parameter is
// recorded as ast.SelfType. See docs/TRAITS.md.
func TestTraitDeclParses(t *testing.T) {
	prog, err := Parse(`trait Display {
    function to_string(self: Self): string;
    function debug(self: Self, verbose: boolean): string;
}`)
	if err != nil {
		t.Fatalf("trait decl should parse: %v", err)
	}
	if len(prog.Traits) != 1 {
		t.Fatalf("expected 1 trait, got %d", len(prog.Traits))
	}
	td := prog.Traits[0]
	if td.Name != "Display" || len(td.Methods) != 2 {
		t.Fatalf("trait = %+v", td)
	}
	if _, ok := td.Methods[0].Params[0].Type.(ast.SelfType); !ok {
		t.Errorf("first param type should be SelfType, got %T", td.Methods[0].Params[0].Type)
	}
	if _, ok := td.Methods[0].Result.(ast.StringType); !ok {
		t.Errorf("to_string result should be string, got %s", td.Methods[0].Result)
	}
	if td.Methods[1].Name != "debug" || len(td.Methods[1].Params) != 2 {
		t.Errorf("debug method = %+v", td.Methods[1])
	}
}

// An `impl Trait for Type` desugars each method into an ordinary
// receiver-method FuncDecl (with Self replaced by the concrete type)
// appended to Program.Funcs, plus an ImplDecl record. See docs/TRAITS.md.
func TestImplDeclDesugarsToReceiverMethods(t *testing.T) {
	prog, err := Parse(`trait Display { function to_string(self: Self): string; }
struct Point { x: i32, y: i32 }
impl Display for Point {
    function to_string(self: Self): string { return "p"; }
}`)
	if err != nil {
		t.Fatalf("impl decl should parse: %v", err)
	}
	if len(prog.Impls) != 1 {
		t.Fatalf("expected 1 impl, got %d", len(prog.Impls))
	}
	impl := prog.Impls[0]
	if impl.Trait != "Display" {
		t.Errorf("impl.Trait = %q, want Display", impl.Trait)
	}
	if st, ok := impl.Type.(ast.StructType); !ok || st.Name != "Point" {
		t.Errorf("impl.Type = %s, want Point", impl.Type)
	}
	// The method should be a receiver-method on Point with Self
	// substituted away.
	var found *ast.FuncDecl
	for _, fn := range prog.Funcs {
		if fn.Name == "to_string" {
			found = fn
		}
	}
	if found == nil {
		t.Fatalf("impl method not appended to Program.Funcs")
	}
	if found.Receiver == nil {
		t.Fatalf("impl method should carry a synthesised receiver")
	}
	if st, ok := found.Receiver.Type.(ast.StructType); !ok || st.Name != "Point" {
		t.Errorf("receiver type = %s, want Point (Self substituted)", found.Receiver.Type)
	}
	if _, ok := found.Receiver.Type.(ast.SelfType); ok {
		t.Error("Self should have been substituted in the receiver")
	}
}

// Error cases: trait method without a `self` first param, and `impl`
// missing the `for` clause.
func TestTraitImplParseErrors(t *testing.T) {
	if _, err := Parse(`trait T { function f(x: i32): i32; }`); err == nil {
		t.Error("trait method without `self` first param should be a parse error")
	}
	if _, err := Parse(`impl T Point { }`); err == nil {
		t.Error("`impl T Point` without `for` should be a parse error")
	}
	if _, err := Parse(`trait T { function f(self: Self): void; }
impl T for Self { function f(self: Self): void {} }`); err == nil {
		t.Error("`impl T for Self` should be a parse error")
	}
}

// Type-parameter trait bounds parse into FuncDecl.Bounds:
// `function f[T: Display + Eq, U: Ord](…)`. See docs/TRAITS.md.
func TestTypeParamBoundsParse(t *testing.T) {
	prog, err := Parse(`function show[T: Display + Eq, U: Ord](a: T, b: U): string { return "x"; }`)
	if err != nil {
		t.Fatalf("bounded type params should parse: %v", err)
	}
	fn := prog.Funcs[0]
	if len(fn.TypeParams) != 2 || fn.TypeParams[0] != "T" || fn.TypeParams[1] != "U" {
		t.Fatalf("type params = %v", fn.TypeParams)
	}
	if got := fn.Bounds["T"]; len(got) != 2 || got[0] != "Display" || got[1] != "Eq" {
		t.Errorf("Bounds[T] = %v, want [Display Eq]", got)
	}
	if got := fn.Bounds["U"]; len(got) != 1 || got[0] != "Ord" {
		t.Errorf("Bounds[U] = %v, want [Ord]", got)
	}
}

// An unbounded type param records no bounds entry.
func TestTypeParamNoBounds(t *testing.T) {
	prog, err := Parse(`function id[T](x: T): T { return x; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(prog.Funcs[0].Bounds) != 0 {
		t.Errorf("expected no bounds, got %v", prog.Funcs[0].Bounds)
	}
}

// Trait references may be module-qualified in impls and in bounds
// (`impl mod.Trait for T`, `[T: mod.Trait]`). modload rewrites the
// qualifier to the imported module's prefix. See docs/TRAITS.md (Phase 3).
func TestQualifiedTraitNames(t *testing.T) {
	prog, err := Parse(`impl shapes.Area for Square { function area(self: Self): i32 { return 1; } }
function f[T: shapes.Area + cmp.Ord](v: T): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("qualified trait names should parse: %v", err)
	}
	if prog.Impls[0].Trait != "shapes.Area" {
		t.Errorf("impl trait = %q, want shapes.Area", prog.Impls[0].Trait)
	}
	fn := prog.Funcs[len(prog.Funcs)-1]
	got := fn.Bounds["T"]
	if len(got) != 2 || got[0] != "shapes.Area" || got[1] != "cmp.Ord" {
		t.Errorf("bounds = %v, want [shapes.Area cmp.Ord]", got)
	}
}

// `@derive(Trait, …)` on a struct records the (possibly qualified)
// trait names on StructDecl.Derives. See docs/TRAITS.md.
func TestDeriveAttributeParses(t *testing.T) {
	prog, err := Parse(`@derive(cmp.Eq, cmp.Display)
struct Point { x: i32, y: i32 }`)
	if err != nil {
		t.Fatalf("@derive should parse: %v", err)
	}
	sd := prog.Structs[0]
	if len(sd.Derives) != 2 || sd.Derives[0] != "cmp.Eq" || sd.Derives[1] != "cmp.Display" {
		t.Errorf("Derives = %v, want [cmp.Eq cmp.Display]", sd.Derives)
	}
	// @derive before `pub struct` works too.
	prog2, err := Parse(`@derive(Eq) pub struct P { x: i32 }`)
	if err != nil {
		t.Fatalf("@derive pub struct: %v", err)
	}
	if !prog2.Structs[0].Public || len(prog2.Structs[0].Derives) != 1 {
		t.Errorf("expected public struct with one derive, got %+v", prog2.Structs[0])
	}
	// @derive on a non-struct is rejected.
	if _, err := Parse(`@derive(Eq) function f(): i32 { return 0; }`); err == nil {
		t.Error("@derive on a function should be a parse error")
	}
	// Unknown attribute name.
	if _, err := Parse(`@frobnicate(Eq) struct S { x: i32 }`); err == nil {
		t.Error("@frobnicate should be rejected")
	}
}

// @derive applies to enums too.
func TestDeriveEnumParses(t *testing.T) {
	prog, err := Parse(`@derive(cmp.Eq, cmp.Display)
enum Color { Red, Green, Blue }`)
	if err != nil {
		t.Fatalf("@derive enum should parse: %v", err)
	}
	if len(prog.Enums) != 1 || len(prog.Enums[0].Derives) != 2 {
		t.Errorf("enum Derives = %v", prog.Enums[0].Derives)
	}
}
