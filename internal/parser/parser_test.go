package parser

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/diag"
)

// TestNumericLiteralErrorsCarryCode pins that invalid numeric literals report
// the P002 ("numeric literal error") code. An uncoded `error:` leaves
// `fern -explain` unable to speak to a code the parser just reported.
func TestNumericLiteralErrorsCarryCode(t *testing.T) {
	codeOf := func(src string) string {
		t.Helper()
		_, err := Parse(src)
		if err == nil {
			t.Fatalf("expected a parse error for %q", src)
		}
		errs, ok := err.(diag.Errors)
		if !ok {
			t.Fatalf("expected diag.Errors, got %T", err)
		}
		for _, e := range errs {
			if c, ok := e.(interface{ Code() string }); ok && c.Code() != "" {
				return c.Code()
			}
		}
		return ""
	}
	if c := codeOf(`function main(): i32 { return 99999999999999999999999; }`); c != "P002" {
		t.Errorf("out-of-range integer literal: code = %q, want P002", c)
	}
	if c := codeOf(`function main(): i32 { return 0xFFFFFFFFFFFFFFFFFFFF; }`); c != "P002" {
		t.Errorf("out-of-range hex literal: code = %q, want P002", c)
	}
	// Float literals are range-checked at f64 width, whatever the suffix says
	// a magnitude no double can hold is P002, not a silent +Inf. The
	// self-host front end has to agree, which is what #6842 was about, so both
	// sides of the boundary are pinned here as well as there.
	if c := codeOf(`function main(): i32 { var x: f64 = 1e309; return 0; }`); c != "P002" {
		t.Errorf("out-of-range float literal: code = %q, want P002", c)
	}
	if c := codeOf(`function main(): i32 { var x: f32 = 1e400f32; return 0; }`); c != "P002" {
		t.Errorf("out-of-range suffixed float literal: code = %q, want P002", c)
	}
	// The accepted side. The boundary is decided by round-to-nearest, so the
	// largest finite double and the spelling just below the tie above it are
	// both valid; UNDERFLOW is not a range error at all (strconv returns a
	// subnormal / ±0 with no error); and f32's range does not gate a literal.
	for _, src := range []string{
		`function main(): i32 { var x: f64 = 1.7976931348623157e308; return 0; }`,
		`function main(): i32 { var x: f64 = 1.7976931348623158e308; return 0; }`,
		`function main(): i32 { var x: f64 = 1e-400; return 0; }`,
		`function main(): i32 { var x: f64 = 5e-324; return 0; }`,
		`function main(): i32 { var x: f32 = 3.5e38; return 0; }`,
	} {
		if _, err := Parse(src); err != nil {
			t.Errorf("%s: unexpected parse error: %v", src, err)
		}
	}
}

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

// `defer EXPR;` and `errdefer EXPR;` both parse to *ast.Defer; the
// `errdefer` form sets OnError so the IR / interp restrict its cleanup
// to error exits.
func TestParseDeferAndErrDefer(t *testing.T) {
	prog, err := Parse(`function f(): Result[i32, i32] {
		defer a();
		errdefer b();
		return Ok(0);
	}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	stmts := prog.Funcs[0].Body.Stmts
	d0, ok := stmts[0].(*ast.Defer)
	if !ok {
		t.Fatalf("stmt 0: expected *ast.Defer, got %T", stmts[0])
	}
	if d0.OnError {
		t.Errorf("`defer` should have OnError=false")
	}
	d1, ok := stmts[1].(*ast.Defer)
	if !ok {
		t.Fatalf("stmt 1: expected *ast.Defer, got %T", stmts[1])
	}
	if !d1.OnError {
		t.Errorf("`errdefer` should have OnError=true")
	}
}

// Block-shaped `defer { … }` / `errdefer { … }` (#5153) parse to *ast.Defer
// whose Expr is a value-less *ast.BlockExpr (the block's action), matching the
// self-host parser. No trailing `;` follows the closing brace.
func TestParseDeferBlockForm(t *testing.T) {
	prog, err := Parse(`function f(): Result[i32, i32] {
		var x: i32 = 0;
		defer { x = x + 1; }
		errdefer { x = x + 2; }
		return Ok(x);
	}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	stmts := prog.Funcs[0].Body.Stmts
	for i, want := range []bool{false, true} { // stmt 1 = defer, stmt 2 = errdefer
		d, ok := stmts[i+1].(*ast.Defer)
		if !ok {
			t.Fatalf("stmt %d: expected *ast.Defer, got %T", i+1, stmts[i+1])
		}
		if d.OnError != want {
			t.Errorf("stmt %d: OnError = %v, want %v", i+1, d.OnError, want)
		}
		blk, ok := d.Expr.(*ast.BlockExpr)
		if !ok {
			t.Fatalf("stmt %d: expected Defer.Expr *ast.BlockExpr, got %T", i+1, d.Expr)
		}
		if blk.Tail != nil {
			t.Errorf("stmt %d: block action should be value-less (Tail == nil)", i+1)
		}
		if len(blk.Stmts) != 1 {
			t.Errorf("stmt %d: expected 1 stmt in block, got %d", i+1, len(blk.Stmts))
		}
	}
}

// TestParseAssertDesugar pins that `assert(cond)` / `assert(cond, msg)`
// desugars, at parse time, to `if (!cond) { eprint(<text>); exit(1); }`
// (#4416) — so no dedicated AST node, checker rule, or codegen is needed;
// it runs on every backend through the existing if/eprint/exit path.
func TestParseAssertDesugar(t *testing.T) {
	prog, err := Parse(`function main(): i32 {
		assert(1 > 0, "msg");
		assert(2 > 1);
		return 0;
	}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	stmts := prog.Funcs[0].Body.Stmts

	// Both asserts become If statements with a unary-! condition and a
	// two-statement then-block (eprint(...) then exit(1)); no else.
	check := func(idx int, wantConcat bool) {
		t.Helper()
		iff, ok := stmts[idx].(*ast.If)
		if !ok {
			t.Fatalf("stmt %d: expected *ast.If, got %T", idx, stmts[idx])
		}
		if iff.Else != nil {
			t.Errorf("stmt %d: assert desugar must have no else", idx)
		}
		u, ok := iff.Cond.(*ast.Unary)
		if !ok || u.Op != "!" {
			t.Fatalf("stmt %d: cond should be unary `!`, got %T %v", idx, iff.Cond, iff.Cond)
		}
		blk, ok := iff.Then.(*ast.Block)
		if !ok || len(blk.Stmts) != 2 {
			t.Fatalf("stmt %d: then should be a 2-stmt block, got %T", idx, iff.Then)
		}
		// First stmt: eprint(<text>). The message form uses string `+`.
		ep, ok := blk.Stmts[0].(*ast.ExprStmt)
		if !ok {
			t.Fatalf("stmt %d: then[0] not an ExprStmt", idx)
		}
		epCall, ok := ep.Expr.(*ast.Call)
		if !ok {
			t.Fatalf("stmt %d: then[0] not a Call", idx)
		}
		if id, ok := epCall.Callee.(*ast.Ident); !ok || id.Name != "eprint" {
			t.Errorf("stmt %d: then[0] callee should be eprint, got %v", idx, epCall.Callee)
		}
		_, isConcat := epCall.Args[0].(*ast.Binary)
		if isConcat != wantConcat {
			t.Errorf("stmt %d: eprint arg concat = %v, want %v", idx, isConcat, wantConcat)
		}
		// Second stmt: exit(1).
		ex, ok := blk.Stmts[1].(*ast.ExprStmt)
		if !ok {
			t.Fatalf("stmt %d: then[1] not an ExprStmt", idx)
		}
		exCall, ok := ex.Expr.(*ast.Call)
		if !ok {
			t.Fatalf("stmt %d: then[1] not a Call", idx)
		}
		if id, ok := exCall.Callee.(*ast.Ident); !ok || id.Name != "exit" {
			t.Errorf("stmt %d: then[1] callee should be exit, got %v", idx, exCall.Callee)
		}
	}
	check(0, true)  // has a message → "assertion failed: " + msg
	check(1, false) // no message → bare "assertion failed" literal

	// `assert` is only special in statement position with a following `(`;
	// as an ordinary identifier it still parses fine.
	if _, err := Parse(`function main(): i32 { var assert: i32 = 5; return assert; }`); err != nil {
		t.Errorf("`assert` as an identifier should still parse: %v", err)
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
	_, err := Parse("function f(): void { f() = 1; }")
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

// `for x in b.items { body }` — the iterator is a struct field access, so the
// trailing `{` opens the loop body, NOT a `b.items { … }` qualified struct
// literal. Regression for the struct-lit clash: a `.field` postfix followed by
// `{` was parsed as a qualified struct literal even in a `noStructLit` (for /
// if / while header) position, mis-reading the body brace.
func TestForEachOverStructFieldDesugars(t *testing.T) {
	prog, err := Parse(`struct Bag { items: i32[] }
	function f(): i32 {
		var b = Bag { items: [1, 2, 3] };
		var sum: i32 = 0;
		for x in b.items {
			sum = sum + x;
		}
		return sum;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	body := prog.Funcs[0].Body.Stmts
	blk, ok := body[2].(*ast.Block)
	if !ok {
		t.Fatalf("foreach over a struct field should desugar to Block, got %T", body[2])
	}
	// The iter (first synthetic var) binds the field access `b.items`, not a
	// struct literal.
	iterVar, ok := blk.Stmts[0].(*ast.Var)
	if !ok {
		t.Fatalf("first inner stmt should bind the iter, got %T", blk.Stmts[0])
	}
	if _, ok := iterVar.Init.(*ast.FieldAccess); !ok {
		t.Fatalf("foreach iter should be a FieldAccess (b.items), got %T", iterVar.Init)
	}
}

// `for i in LOW..HIGH { body }` desugars to a Block of `{ var
// __range_hi = HIGH; for (var i = LOW; i < __range_hi; i = i + 1)
// { body } }` — HIGH bound once, a For (not While) so `continue`
// advances via the step.
func TestForInRangeDesugars(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		var sum: i32 = 0;
		for i in 0..5 {
			sum = sum + i;
		}
		return sum;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	blk, ok := prog.Funcs[0].Body.Stmts[1].(*ast.Block)
	if !ok {
		t.Fatalf("range-for should desugar to Block, got %T", prog.Funcs[0].Body.Stmts[1])
	}
	if len(blk.Stmts) != 2 {
		t.Fatalf("expected 2 inner stmts (hi-bind / for), got %d", len(blk.Stmts))
	}
	if _, ok := blk.Stmts[0].(*ast.Var); !ok {
		t.Errorf("first stmt should bind HIGH once (Var), got %T", blk.Stmts[0])
	}
	loop, ok := blk.Stmts[1].(*ast.For)
	if !ok {
		t.Fatalf("second stmt should be a For (so continue advances), got %T", blk.Stmts[1])
	}
	if loop.Init == nil || loop.Cond == nil || loop.Step == nil {
		t.Errorf("desugared For must have Init/Cond/Step; got %+v", loop)
	}
	if b, ok := loop.Cond.(*ast.Binary); !ok || b.Op != "<" {
		t.Errorf("range loop cond should be `i < hi`, got %T %v", loop.Cond, loop.Cond)
	}
}

// `for i in LOW..=HIGH { body }` is the inclusive (closed-interval)
// range: same desugar as the half-open form bar the loop condition,
// which becomes `i <= hi` so HIGH is itself visited.
func TestForInInclusiveRangeDesugars(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		var sum: i32 = 0;
		for i in 0..=5 {
			sum = sum + i;
		}
		return sum;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	blk, ok := prog.Funcs[0].Body.Stmts[1].(*ast.Block)
	if !ok {
		t.Fatalf("inclusive range-for should desugar to Block, got %T", prog.Funcs[0].Body.Stmts[1])
	}
	if len(blk.Stmts) != 2 {
		t.Fatalf("expected 2 inner stmts (hi-bind / for), got %d", len(blk.Stmts))
	}
	loop, ok := blk.Stmts[1].(*ast.For)
	if !ok {
		t.Fatalf("second stmt should be a For, got %T", blk.Stmts[1])
	}
	if b, ok := loop.Cond.(*ast.Binary); !ok || b.Op != "<=" {
		t.Errorf("inclusive range loop cond should be `i <= hi`, got %T %v", loop.Cond, loop.Cond)
	}
}

// `loop { ... }` parses to a canonical *ast.Loop node — not While-true
// sugar — so divergence analyses can recognize it as definitionally
// diverging without pattern-matching a literal-true While condition.
func TestLoopParsesToCanonicalLoopNode(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		var i: i32 = 0;
		loop { i = i + 1; if (i >= 3) { break; } }
		return i;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	l, ok := prog.Funcs[0].Body.Stmts[1].(*ast.Loop)
	if !ok {
		t.Fatalf("loop should parse to *ast.Loop, got %T", prog.Funcs[0].Body.Stmts[1])
	}
	if _, ok := l.Body.(*ast.Block); !ok {
		t.Errorf("loop Body should be a Block, got %T", l.Body)
	}
}

// A label before a loop is parsed onto the loop node, and labeled
// `break`/`continue` carry the target label.
func TestLabeledLoopsAndBreakContinue(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		outer: while (true) {
			inner: loop {
				break outer;
				continue inner;
			}
		}
		return 0;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	outer, ok := prog.Funcs[0].Body.Stmts[0].(*ast.While)
	if !ok || outer.Label != "outer" {
		t.Fatalf("expected labeled While `outer`, got %T %q", prog.Funcs[0].Body.Stmts[0], labelOf(prog.Funcs[0].Body.Stmts[0]))
	}
	innerBody := outer.Body.(*ast.Block).Stmts
	inner, ok := innerBody[0].(*ast.Loop)
	if !ok || inner.Label != "inner" {
		t.Fatalf("expected labeled *ast.Loop `inner`, got %T", innerBody[0])
	}
	stmts := inner.Body.(*ast.Block).Stmts
	br, ok := stmts[0].(*ast.Break)
	if !ok || br.Label != "outer" {
		t.Errorf("expected `break outer`, got %T %+v", stmts[0], stmts[0])
	}
	cont, ok := stmts[1].(*ast.Continue)
	if !ok || cont.Label != "inner" {
		t.Errorf("expected `continue inner`, got %T %+v", stmts[1], stmts[1])
	}
}

func labelOf(s ast.Stmt) string {
	switch n := s.(type) {
	case *ast.While:
		return n.Label
	case *ast.Loop:
		return n.Label
	case *ast.For:
		return n.Label
	}
	return ""
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

// `float` is the width-unqualified alias for f64 (#5363) — a
// contextual Ident in type position (like `str`), producing
// FloatType{Width:64, Spelling:"float"}. A `float.`-qualified name
// stays a module struct reference.
func TestFloatAliasType(t *testing.T) {
	prog, err := Parse(`function f(x: float): float { var xs: float[] = [x]; return xs[0] as float; }`)
	if err != nil {
		t.Fatal(err)
	}
	fn := prog.Funcs[0]
	for _, ty := range []ast.Type{fn.ReturnType, fn.Params[0].Type} {
		ft, ok := ty.(ast.FloatType)
		if !ok {
			t.Fatalf("type = %T, want FloatType", ty)
		}
		if ft.Width != 64 || ft.Spelling != "float" {
			t.Errorf("got FloatType{Width:%d, Spelling:%q}, want {64, \"float\"}", ft.Width, ft.Spelling)
		}
	}
	at, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Var).Type.(ast.ArrayType)
	if !ok || at.Elem.(ast.FloatType).Width != 64 {
		t.Errorf("var type = %#v, want float[] with f64 elem", prog.Funcs[0].Body.Stmts[0].(*ast.Var).Type)
	}

	prog, err = Parse(`function g(v: float.Vec): i32 { return 0; }`)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := prog.Funcs[0].Params[0].Type.(ast.StructType)
	if !ok || st.Name != "float.Vec" {
		t.Errorf("qualified type = %#v, want StructType{Name:\"float.Vec\"}", prog.Funcs[0].Params[0].Type)
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
// same way as a plain `=` lvalue — into `a.v = a.v + 2`. A compound path
// allowing only Ident/Index targets rejects a field lvalue with P003 even
// though plain `=` accepts it.
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

// Block-expressions (slice 1): an `if`-expression branch with leading
// statements followed by a trailing value expression (no `;`) parses to
// a *ast.BlockExpr whose Stmts hold the statements and Tail the value.
func TestBlockExprIfBranch(t *testing.T) {
	prog, err := Parse(`function f(e: i32): i32 {
		return if (e > 0) { var k = e + 1; k } else { 0 };
	}`)
	if err != nil {
		t.Fatal(err)
	}
	ie := prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value.(*ast.IfExpr)
	be, ok := ie.Then.(*ast.BlockExpr)
	if !ok {
		t.Fatalf("then branch should be *BlockExpr, got %T", ie.Then)
	}
	if len(be.Stmts) != 1 {
		t.Fatalf("expected 1 leading stmt, got %d", len(be.Stmts))
	}
	if _, ok := be.Stmts[0].(*ast.Var); !ok {
		t.Errorf("stmt 0 should be *Var, got %T", be.Stmts[0])
	}
	if be.Tail == nil {
		t.Fatal("tail should be non-nil (the trailing `k`)")
	}
	if id, ok := be.Tail.(*ast.Ident); !ok || id.Name != "k" {
		t.Errorf("tail should be Ident `k`, got %T %v", be.Tail, be.Tail)
	}
	// The else branch is a bare single-expr — decision 3: keeps the
	// no-statement single-expr case as a plain expr, NOT a BlockExpr.
	if _, ok := ie.Else.(*ast.BlockExpr); ok {
		t.Errorf("else `{ 0 }` (single expr, no stmts) should stay a bare expr, got *BlockExpr")
	}
}

// A single-expression branch stays byte-identical to the pre-block-expr
// behaviour: `{ 1 }` is a bare NumberLit, not a BlockExpr.
func TestBlockExprSingleExprUnchanged(t *testing.T) {
	prog, err := Parse(`function f(b: boolean): i32 { return if (b) { 1 } else { 2 }; }`)
	if err != nil {
		t.Fatal(err)
	}
	ie := prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value.(*ast.IfExpr)
	if _, ok := ie.Then.(*ast.NumberLit); !ok {
		t.Errorf("then `{ 1 }` should be a bare NumberLit, got %T", ie.Then)
	}
	if _, ok := ie.Else.(*ast.NumberLit); !ok {
		t.Errorf("else `{ 2 }` should be a bare NumberLit, got %T", ie.Else)
	}
}

// A branch whose final element is a `;`-terminated statement has NO
// trailing value — BlockExpr.Tail is nil (the checker reports E061 when
// it's used in value position).
func TestBlockExprNoTail(t *testing.T) {
	prog, err := Parse(`function f(b: boolean): i32 {
		return if (b) { var k = 1; } else { 0 };
	}`)
	if err != nil {
		t.Fatal(err)
	}
	ie := prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value.(*ast.IfExpr)
	be, ok := ie.Then.(*ast.BlockExpr)
	if !ok {
		t.Fatalf("then branch should be *BlockExpr, got %T", ie.Then)
	}
	if be.Tail != nil {
		t.Errorf("a `;`-terminated final stmt means no tail; got Tail=%v", be.Tail)
	}
	if len(be.Stmts) != 1 {
		t.Errorf("expected 1 stmt, got %d", len(be.Stmts))
	}
}

// `else if` chaining still parses with the block-expr branch path: the
// nested IfExpr lives in the outer's Else slot.
func TestBlockExprElseIfChain(t *testing.T) {
	prog, err := Parse(`function f(n: i32): i32 {
		return if (n == 1) { var a = 10; a } else if (n == 2) { 20 } else { 30 };
	}`)
	if err != nil {
		t.Fatal(err)
	}
	ie := prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value.(*ast.IfExpr)
	if _, ok := ie.Then.(*ast.BlockExpr); !ok {
		t.Errorf("then should be *BlockExpr, got %T", ie.Then)
	}
	if _, ok := ie.Else.(*ast.IfExpr); !ok {
		t.Fatalf("else should be a nested IfExpr (else-if chain), got %T", ie.Else)
	}
}

// A `match`-expression arm body can be a block-expression: the `{ stmts;
// tail }` form parses to a *ast.BlockExpr on the arm, while a bare-expr
// arm body stays unchanged.
func TestBlockExprMatchArm(t *testing.T) {
	prog, err := Parse(`function f(tag: i32): i32 {
		return match (tag) {
			0 => { var s = tag + 5; s },
			_ => 99
		};
	}`)
	if err != nil {
		t.Fatal(err)
	}
	me := prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value.(*ast.MatchExpr)
	be, ok := me.Arms[0].Body.(*ast.BlockExpr)
	if !ok {
		t.Fatalf("arm 0 body should be *BlockExpr, got %T", me.Arms[0].Body)
	}
	if len(be.Stmts) != 1 || be.Tail == nil {
		t.Errorf("arm 0 block: want 1 stmt + tail, got %d stmts, tail=%v", len(be.Stmts), be.Tail)
	}
	if _, ok := me.Arms[1].Body.(*ast.NumberLit); !ok {
		t.Errorf("arm 1 body `99` should stay a bare NumberLit, got %T", me.Arms[1].Body)
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

// Construction-site type arguments — the `[ TypeArgs ]` of the spec
// grammar's `StructLit = QualName [ TypeArgs ] '{' … '}'` (#6812).
func TestStructLitTypeArgsParse(t *testing.T) {
	cases := []struct {
		name     string
		init     string
		typeName string
		args     []string
		hasBase  bool
	}{
		{"one arg", `Box[i32] { val: 1 }`, "Box", []string{"i32"}, false},
		{"two args", `Pair[i32, string] { a: 1, b: "x" }`, "Pair", []string{"i32", "string"}, false},
		{"nested arg", `Box[i32[]] { val: [1] }`, "Box", []string{"i32[]"}, false},
		{"trailing comma", `Box[i32,] { val: 1 }`, "Box", []string{"i32"}, false},
		{"qualified", `m.Box[i32] { val: 1 }`, "m.Box", []string{"i32"}, false},
		{"path qualified", `m::Box[i32] { val: 1 }`, "m.Box", []string{"i32"}, false},
		{"spread", `Box[i32] { ...b, val: 1 }`, "Box", []string{"i32"}, true},
		{"no args", `Box { val: 1 }`, "Box", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := Parse(`function main(): i32 { var v = ` + tc.init + `; return 0; }`)
			if err != nil {
				t.Fatalf("parse %s: %v", tc.init, err)
			}
			sl, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Var).Init.(*ast.StructLit)
			if !ok {
				t.Fatalf("%s: init should be StructLit, got %T",
					tc.init, prog.Funcs[0].Body.Stmts[0].(*ast.Var).Init)
			}
			if sl.TypeName != tc.typeName {
				t.Errorf("TypeName = %q, want %q", sl.TypeName, tc.typeName)
			}
			if sl.TypeArgsWritten != (tc.args != nil) {
				t.Errorf("TypeArgsWritten = %v, want %v", sl.TypeArgsWritten, tc.args != nil)
			}
			if len(sl.TypeArgs) != len(tc.args) {
				t.Fatalf("got %d type args, want %d (%v)", len(sl.TypeArgs), len(tc.args), sl.TypeArgs)
			}
			for i, want := range tc.args {
				if got := sl.TypeArgs[i].String(); got != want {
					t.Errorf("TypeArgs[%d] = %s, want %s", i, got, want)
				}
			}
			if (sl.Base != nil) != tc.hasBase {
				t.Errorf("Base != nil = %v, want %v", sl.Base != nil, tc.hasBase)
			}
		})
	}
}

// The struct-literal type-arg branch must not eat an ordinary index
// whose `]` happens to be followed by a `{` — the disambiguator is the
// type keyword after `[`, plus the same `noStructLit` gate the bare
// `Ident { … }` form uses.
func TestStructLitTypeArgsDoNotClaimIndexing(t *testing.T) {
	prog, err := Parse(`function main(): i32 {
			var xs = [1, 2, 3];
			var n = 0;
			while (xs[0] > 0) { n = n + 1; }
			var y = xs[1];
			return y + n;
		}`)
	if err != nil {
		t.Fatal(err)
	}
	body := prog.Funcs[0].Body.Stmts
	if _, ok := body[2].(*ast.While); !ok {
		t.Fatalf("`while (xs[0] > 0) { … }` should stay a While, got %T", body[2])
	}
	if _, ok := body[3].(*ast.Var).Init.(*ast.Index); !ok {
		t.Errorf("`xs[1]` should stay an Index, got %T", body[3].(*ast.Var).Init)
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

// `pub(package) function f` sets PackageScoped (not Public). See
// docs/PUB-PACKAGE.md.
func TestPubPackageSetsPackageScoped(t *testing.T) {
	prog, err := Parse(`pub(package) function helper(): i32 { return 1; }
pub function exposed(): i32 { return 2; }
pub(package) const K: i32 = 3;`)
	if err != nil {
		t.Fatal(err)
	}
	var helper, exposed *ast.FuncDecl
	for _, fn := range prog.Funcs {
		switch fn.Name {
		case "helper":
			helper = fn
		case "exposed":
			exposed = fn
		}
	}
	if helper == nil || !helper.PackageScoped || helper.Public {
		t.Errorf("helper should be PackageScoped and not Public; got %+v", helper)
	}
	if exposed == nil || exposed.PackageScoped || !exposed.Public {
		t.Errorf("exposed should be Public and not PackageScoped; got %+v", exposed)
	}
	if len(prog.Consts) != 1 || !prog.Consts[0].PackageScoped || prog.Consts[0].Public {
		t.Errorf("const K should be PackageScoped; got %+v", prog.Consts)
	}
	// `pub(foo)` (anything but package) is a parse error.
	if _, err := Parse(`pub(crate) function f(): i32 { return 1; }`); err == nil {
		t.Error("`pub(crate)` should be a parse error")
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

// TestDecimalLiteralOverflowIsError — a decimal literal exceeding the
// 64-bit range must be reported, not silently wrapped two's-complement.
// The old hand-rolled `n = n*10 + digit` overflowed without a
// diagnostic, and the wrapped value (which happened to fit i64) slipped
// past the checker's range check. Regression for F3 in
// docs/ADVERSARIAL-REVIEW-2026-06.md.
func TestDecimalLiteralOverflowIsError(t *testing.T) {
	_, err := Parse(`function main(): i64 { return 99999999999999999999999999; }`)
	if err == nil {
		t.Fatal("expected parse error for out-of-range decimal literal")
	}
	if !strings.Contains(err.Error(), "integer literal") {
		t.Errorf("error should mention the integer literal; got %v", err)
	}
}

// TestLargeU64DecimalLiteralParses — a decimal literal above i64 max but
// within u64 range is still accepted via the unsigned fallback (u64 max
// = 18446744073709551615). Guards that the overflow check didn't reject
// legitimate large unsigned literals.
func TestLargeU64DecimalLiteralParses(t *testing.T) {
	if _, err := Parse(`function main(): u64 { return 18446744073709551615u64; }`); err != nil {
		t.Fatalf("u64-max literal should parse: %v", err)
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

// A named-field variant (`Rect { w: i32, h: i32 }`) parses with
// FieldNames parallel to Payloads; the positional form leaves FieldNames
// empty. See docs/NAMED-FIELD-VARIANTS.md.
func TestNamedFieldEnumVariantParses(t *testing.T) {
	prog, err := Parse(`enum Shape { Circle { r: i32 }, Rect { w: i32, h: i32 }, Unit, Pair(i32, i32) }`)
	if err != nil {
		t.Fatal(err)
	}
	vs := prog.Enums[0].Variants
	if len(vs) != 4 {
		t.Fatalf("expected 4 variants, got %d", len(vs))
	}
	if len(vs[0].FieldNames) != 1 || vs[0].FieldNames[0] != "r" || len(vs[0].Payloads) != 1 {
		t.Errorf("Circle = %+v, want field r", vs[0])
	}
	if len(vs[1].FieldNames) != 2 || vs[1].FieldNames[0] != "w" || vs[1].FieldNames[1] != "h" {
		t.Errorf("Rect = %+v, want fields w, h", vs[1])
	}
	if len(vs[2].FieldNames) != 0 || len(vs[2].Payloads) != 0 {
		t.Errorf("Unit should be payloadless, got %+v", vs[2])
	}
	if len(vs[3].FieldNames) != 0 || len(vs[3].Payloads) != 2 {
		t.Errorf("Pair should stay positional, got %+v", vs[3])
	}
	// Empty named-field body is a parse error.
	if _, err := Parse(`enum E { V {} }`); err == nil {
		t.Error("`V {}` (empty named-field body) should be a parse error")
	}
}

// A named-field match pattern `Rect { w, h }` sets NamedFields with the
// field names as bindings.
func TestNamedFieldMatchPatternParses(t *testing.T) {
	prog, err := Parse(`enum Shape { Rect { w: i32, h: i32 } }
function f(s: Shape): i32 {
    match (s) {
        Rect { w, h } => { return w + h; },
    }
    return 0;
}`)
	if err != nil {
		t.Fatal(err)
	}
	var m *ast.Match
	for _, st := range prog.Funcs[0].Body.Stmts {
		if mm, ok := st.(*ast.Match); ok {
			m = mm
		}
	}
	if m == nil {
		t.Fatal("no match stmt found")
	}
	arm := m.Arms[0]
	if !arm.NamedFields {
		t.Errorf("arm should be NamedFields")
	}
	if len(arm.Bindings) != 2 || arm.Bindings[0] != "w" || arm.Bindings[1] != "h" {
		t.Errorf("arm bindings = %v, want [w h]", arm.Bindings)
	}
}

// A named field can carry a SUB-PATTERN rather than a binder —
// `P { x: 0, y }` — matched against the field's value. It reuses the payload
// slot's mechanism: the slot binds a synthetic temp and the arm's body nests
// inside a match on it, so the merged arm keeps projecting by field.
func TestStructFieldSubPatternParses(t *testing.T) {
	for name, src := range map[string]string{
		"literal": `struct P { x: i32, y: i32 }
function f(p: P): i32 {
    match (p) { P { x: 0, y } => { return y; }, P { x, y } => { return x + y; } }
}`,
		"variant": `enum E { A(i32), B }
struct W { e: E, n: i32 }
function f(w: W): i32 {
    match (w) { W { e: A(v), n } => { return v + n; }, W { e: B(), n } => { return n; } }
}`,
		"tuple": `struct T { p: (i32, i32), n: i32 }
function f(t: T): i32 {
    match (t) { T { p: (0, b), n } => { return b + n; }, T { p: (a, b), n } => { return a + b + n; } }
}`,
		"nested struct": `struct P { x: i32, y: i32 }
struct N { inner: P, n: i32 }
function f(v: N): i32 {
    match (v) { N { inner: P { x: 0, y }, n } => { return y + n; }, N { inner: P { x, y }, n } => { return x + y + n; } }
}`,
		"expression form": `struct P { x: i32, y: i32 }
function f(p: P): i32 { return match (p) { P { x: 0, y } => y, P { x, y } => x + y }; }`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(src); err != nil {
				t.Errorf("should parse: %v", err)
			}
		})
	}
}

// The merged arm carries ONE field list and one temp per slot, so arms in a
// nested-field group that project different fields would bind wrong values.
// That is a diagnostic, not a silent miscompile — a positional group cannot
// hit it because a variant's payload arity is fixed.
func TestStructFieldSubPatternFieldListsMustAgree(t *testing.T) {
	_, err := Parse(`struct P { x: i32, y: i32 }
function f(p: P): i32 {
    match (p) {
        P { x: 0, y } => { return y; },
        P { x } => { return x; },
        _ => { return 0; },
    }
}`)
	if err == nil {
		t.Fatal("want a diagnostic for mismatched field lists, got none")
	}
	if !strings.Contains(err.Error(), "same fields in the same order") {
		t.Errorf("want the field-list diagnostic, got: %v", err)
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

// An or-pattern arm (`A | B => body`, #2698) desugars at parse time into one
// MatchArm per alternative, each sharing a cloned guard + body. Exhaustiveness
// and lowering then see an ordinary flat arm list. Verifies the alternatives
// expand, the shared body is cloned (distinct pointers, not aliased), and a
// per-alternative payload binding is preserved.
func TestMatchOrPatternDesugars(t *testing.T) {
	prog, err := Parse(`enum E { A, B(i32), C(i32), D }
function f(e: E): i32 {
	match (e) {
		A | D => { return 0; },
		B(n) | C(n) => { return n; }
	}
	return -1;
}`)
	if err != nil {
		t.Fatal(err)
	}
	var m *ast.Match
	for _, s := range prog.Funcs[0].Body.Stmts {
		if mm, ok := s.(*ast.Match); ok {
			m = mm
			break
		}
	}
	if m == nil {
		t.Fatal("match stmt not found")
	}
	// `A | D` and `B(n) | C(n)` -> 4 flat arms.
	if len(m.Arms) != 4 {
		t.Fatalf("expected 4 desugared arms; got %d", len(m.Arms))
	}
	wantVariant := []string{"A", "D", "B", "C"}
	for i, w := range wantVariant {
		if m.Arms[i].VariantName != w {
			t.Errorf("arm %d variant = %q, want %q", i, m.Arms[i].VariantName, w)
		}
	}
	// Same-name binding preserved on each alternative of `B(n) | C(n)`.
	if len(m.Arms[2].Bindings) != 1 || m.Arms[2].Bindings[0] != "n" {
		t.Errorf("arm B should bind `n`; got %+v", m.Arms[2].Bindings)
	}
	if len(m.Arms[3].Bindings) != 1 || m.Arms[3].Bindings[0] != "n" {
		t.Errorf("arm C should bind `n`; got %+v", m.Arms[3].Bindings)
	}
	// The shared body is cloned per alternative, not aliased — a later pass
	// mutating one arm's body must not affect the other.
	if m.Arms[0].Body == m.Arms[1].Body {
		t.Error("or-pattern alternatives must not share the same *Block pointer")
	}
	if m.Arms[2].Body == m.Arms[3].Body {
		t.Error("or-pattern alternatives must not share the same *Block pointer")
	}
}

// Literal and tuple or-patterns (`1 | 2 => …`, `(0, y) | (y, 0) => …`)
// both parse now (#5355): each alternative is its own pattern, so the
// shared clone-desugar expands them into separate arms. Tuple alternatives
// bind per-alternative (a name may sit in a different element position in
// each), which the clone-desugar handles without a shared binding set.
func TestMatchLiteralOrPatternParses(t *testing.T) {
	if _, err := Parse(`function f(n: i32): i32 {
	return match (n) {
		1 | 2 => 10,
		_ => 0,
	};
}`); err != nil {
		t.Fatalf("literal or-pattern should parse now, got %v", err)
	}
	if _, err := Parse(`function f(t: (i32, i32)): i32 {
	return match (t) {
		(0, y) | (y, 0) => 1,
		_ => 0,
	};
}`); err != nil {
		t.Fatalf("tuple or-pattern should parse now, got %v", err)
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

// `stream[T]` parses to the built-in ast.StreamType (the WASI Preview-3 async
// data channel — docs/STREAM-TYPE-SURFACE.md), NOT a generic enum
// instantiation, distinguished contextually by the name `stream`. A bare
// `stream` (no bracket arg) and a generic `stream[A, B]` (two args) stay an
// ordinary struct/enum reference, so `stream` remains a usable identifier.
func TestStreamTypeParse(t *testing.T) {
	prog, err := Parse(`function f(s: stream[u8]): i32 { return 0; }
function g(): stream[string] { return None; }
function h(xs: stream[u8][]): i32 { return 0; }`)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := prog.Funcs[0].Params[0].Type.(ast.StreamType)
	if !ok {
		t.Fatalf("f's param should be stream[u8]; got %+v", prog.Funcs[0].Params[0].Type)
	}
	if n, ok := st.Elem.(ast.NumberType); !ok || n.NormalWidth() != 8 {
		t.Errorf("stream element should be u8; got %+v", st.Elem)
	}
	if st.String() != "stream[u8]" {
		t.Errorf("StreamType.String() = %q, want stream[u8]", st.String())
	}
	rt, ok := prog.Funcs[1].ReturnType.(ast.StreamType)
	if !ok {
		t.Fatalf("g's return type should be stream[string]; got %+v", prog.Funcs[1].ReturnType)
	}
	if _, ok := rt.Elem.(ast.StringType); !ok {
		t.Errorf("stream element should be string; got %+v", rt.Elem)
	}
	// `stream[u8][]` is an array of streams (the array suffix wraps the stream).
	at, ok := prog.Funcs[2].Params[0].Type.(ast.ArrayType)
	if !ok {
		t.Fatalf("h's param should be an array; got %+v", prog.Funcs[2].Params[0].Type)
	}
	if _, ok := at.Elem.(ast.StreamType); !ok {
		t.Errorf("h's array element should be stream[u8]; got %+v", at.Elem)
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

// A nested tuple position binds too: `let (a, (b, c)) = t;`. The inner level
// becomes its own *ast.Destructure hanging off Nested[i], reading the
// synthesised binder this level put in Names[i].
func TestNestedTupleDestructureParses(t *testing.T) {
	prog, err := Parse(`function f(t: (i32, (i32, (i32, i32)))): i32 {
		let (a, (b, (c, d))) = t;
		return a + b + c + d;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Destructure)
	if !ok {
		t.Fatalf("first stmt should be *ast.Destructure; got %T", prog.Funcs[0].Body.Stmts[0])
	}
	if len(d.Names) != 2 || d.Names[0] != "a" {
		t.Fatalf("outer names = %v, want [a <synth>]", d.Names)
	}
	if len(d.Nested) != 2 || d.Nested[0] != nil || d.Nested[1] == nil {
		t.Fatalf("only position 1 should nest: %+v", d.Nested)
	}
	// The invariant every Init-only pass relies on: a nested level's Init is
	// the ident naming this level's synthesised binder.
	assertNestedInit := func(parent *ast.Destructure, i int) *ast.Destructure {
		t.Helper()
		sub := parent.Nested[i]
		id, ok := sub.Init.(*ast.Ident)
		if !ok {
			t.Fatalf("nested Init should be an *ast.Ident; got %T", sub.Init)
		}
		if id.Name != parent.Names[i] {
			t.Fatalf("nested Init reads %q, want the parent's binder %q", id.Name, parent.Names[i])
		}
		return sub
	}
	mid := assertNestedInit(d, 1)
	if len(mid.Names) != 2 || mid.Names[0] != "b" {
		t.Fatalf("depth-2 names = %v, want [b <synth>]", mid.Names)
	}
	inner := assertNestedInit(mid, 1)
	if len(inner.Names) != 2 || inner.Names[0] != "c" || inner.Names[1] != "d" {
		t.Fatalf("depth-3 names = %v, want [c d]", inner.Names)
	}
	if inner.Nested != nil {
		t.Errorf("the innermost level should not nest: %+v", inner.Nested)
	}
}

// Two `_` discards at different levels get different internal names. Every
// level of one pattern shares the statement's source position, so naming a
// discard by position alone made the second one a redeclaration of the first.
func TestNestedTupleDestructureDiscardsDoNotCollide(t *testing.T) {
	prog, err := Parse(`function f(t: (i32, (i32, i32))): i32 {
		let (_, (_, c)) = t;
		return c;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	d := prog.Funcs[0].Body.Stmts[0].(*ast.Destructure)
	outer, inner := d.Names[0], d.Nested[1].Names[0]
	if outer == inner {
		t.Fatalf("both discards named %q", outer)
	}
	for _, nm := range []string{outer, inner} {
		if !strings.HasPrefix(nm, "__discard_") {
			t.Errorf("discard named %q, want a __discard_ name", nm)
		}
	}
}

// A destructuring PARAMETER takes the same production, so it nests too.
func TestNestedTupleDestructureParam(t *testing.T) {
	prog, err := Parse(`function f((a, (b, c)): (i32, (i32, i32))): i32 { return a + b + c; }`)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Destructure)
	if !ok {
		t.Fatalf("the param destructure should be prepended to the body; got %T", prog.Funcs[0].Body.Stmts[0])
	}
	if len(d.Nested) != 2 || d.Nested[1] == nil {
		t.Fatalf("param position 1 should nest: %+v", d.Nested)
	}
	if got := d.Nested[1].Names; len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Errorf("nested param names = %v, want [b c]", got)
	}
}

// A singleton stays a parse error at every depth — the no-singleton-tuples
// rule is about the pattern, not about which level it sits on.
func TestNestedTupleDestructureSingletonError(t *testing.T) {
	if _, err := Parse(`function f(t: (i32, (i32, i32))): i32 { let (a, (b)) = t; return a; }`); err == nil {
		t.Error("expected parse error for a singleton nested position")
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

// `let Point { x, y: b, .. } = p;` parses to a *ast.Destructure with
// Fields set (struct mode), StructName recorded, `y: b` renamed, and the
// trailing `..` accepted (partial bind). Shares the node with the tuple
// form so the checker / interp / IR reuse the same lowering.
func TestStructDestructureParses(t *testing.T) {
	prog, err := Parse(`struct Point { x: i32, y: i32, z: i32 }
	function f(): i32 {
		var p: Point = Point { x: 1, y: 2, z: 3 };
		let Point { x, y: b, .. } = p;
		return x + b;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	stmts := prog.Funcs[0].Body.Stmts
	d, ok := stmts[1].(*ast.Destructure)
	if !ok {
		t.Fatalf("second stmt should be *ast.Destructure; got %T", stmts[1])
	}
	if d.StructName != "Point" {
		t.Errorf("StructName = %q, want Point", d.StructName)
	}
	if len(d.Fields) != 2 || d.Fields[0] != "x" || d.Fields[1] != "y" {
		t.Errorf("Fields = %v, want [x y]", d.Fields)
	}
	if len(d.Names) != 2 || d.Names[0] != "x" || d.Names[1] != "b" {
		t.Errorf("Names = %v, want [x b]", d.Names)
	}
}

// `var Point { x, y } = p;` parses the same way via the `var` keyword.
func TestStructDestructureVarKeyword(t *testing.T) {
	prog, err := Parse(`struct Point { x: i32, y: i32 }
	function f(): i32 {
		var p: Point = Point { x: 1, y: 2 };
		var Point { x, y } = p;
		return x + y;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := prog.Funcs[0].Body.Stmts[1].(*ast.Destructure)
	if !ok || d.Fields == nil {
		t.Fatalf("second stmt should be a struct-mode *ast.Destructure; got %T", prog.Funcs[0].Body.Stmts[1])
	}
}

// TestTupleParamDestructureParses pins the parse-time desugar of a
// tuple-destructuring parameter `(a, b): (T, U)`: the param list gets
// a synthetic `__ptuple_<line>_<col>` holder of the annotated type and
// the body is prepended with `let (a, b) = <synth>;` (an ordinary
// ast.Destructure whose Init is the synthetic param), so the checker /
// interp / IR reuse the statement-destructure path unchanged.
func TestTupleParamDestructureParses(t *testing.T) {
	prog, err := Parse(`function add((a, b): (i32, i32), k: i32): i32 {
		return a + b + k;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	fn := prog.Funcs[0]
	if len(fn.Params) != 2 {
		t.Fatalf("params = %d, want 2", len(fn.Params))
	}
	if !strings.HasPrefix(fn.Params[0].Name, "__ptuple_") {
		t.Errorf("synthetic param name = %q, want __ptuple_ prefix", fn.Params[0].Name)
	}
	if _, ok := fn.Params[0].Type.(ast.TupleType); !ok {
		t.Errorf("synthetic param type = %T, want ast.TupleType", fn.Params[0].Type)
	}
	if fn.Params[1].Name != "k" {
		t.Errorf("second param = %q, want k", fn.Params[1].Name)
	}
	d, ok := fn.Body.Stmts[0].(*ast.Destructure)
	if !ok {
		t.Fatalf("first body stmt should be *ast.Destructure; got %T", fn.Body.Stmts[0])
	}
	if len(d.Names) != 2 || d.Names[0] != "a" || d.Names[1] != "b" {
		t.Errorf("names = %v, want [a b]", d.Names)
	}
	id, ok := d.Init.(*ast.Ident)
	if !ok || id.Name != fn.Params[0].Name {
		t.Errorf("destructure Init = %#v, want Ident %q", d.Init, fn.Params[0].Name)
	}
}

// Both lambda forms accept a tuple-destructuring parameter and get the
// same body-prelude desugar as named functions.
func TestTupleParamDestructureLambdas(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		var g = function((x, y): (i32, i32)): i32 { return x * y; };
		var h = ((lo, hi): (i32, i32)) => hi - lo;
		return g((2, 3)) + h((1, 5));
	}`)
	if err != nil {
		t.Fatal(err)
	}
	stmts := prog.Funcs[0].Body.Stmts
	for i, name := range []string{"g", "h"} {
		v, ok := stmts[i].(*ast.Var)
		if !ok {
			t.Fatalf("stmt %d should be *ast.Var %s; got %T", i, name, stmts[i])
		}
		lam, ok := v.Init.(*ast.Lambda)
		if !ok {
			t.Fatalf("%s should bind a *ast.Lambda; got %T", name, v.Init)
		}
		if len(lam.Params) != 1 || !strings.HasPrefix(lam.Params[0].Name, "__ptuple_") {
			t.Errorf("%s params = %v, want one synthetic __ptuple_ param", name, lam.Params)
		}
		if _, ok := lam.Body.Stmts[0].(*ast.Destructure); !ok {
			t.Errorf("%s first body stmt = %T, want *ast.Destructure", name, lam.Body.Stmts[0])
		}
	}
}

// Error shapes: single-name pattern, default value on a destructured
// param, and a body-less (@import) decl are all parse errors.
func TestTupleParamDestructureErrors(t *testing.T) {
	cases := map[string]string{
		"single-name": `function f((a): (i32, i32)): i32 { return a; }`,
		"default":     `function f((a, b): (i32, i32) = (1, 2)): i32 { return a; }`,
		"bodyless":    `function f((a, b): (i32, i32)): i32;`,
	}
	for name, src := range cases {
		if _, err := Parse(src); err == nil {
			t.Errorf("%s: expected parse error", name)
		}
	}
}

// TestTupleMatchPatternParses pins the tuple-pattern arm shape
// `(p0, p1, …) => …`: each element is a binder, `_`, or a literal,
// carried on MatchArm.TupleElems for the checker/IR.
func TestTupleMatchPatternParses(t *testing.T) {
	prog, err := Parse(`function f(p: (i32, i32)): i32 {
		match (p) {
			(0, y) => { return y; },
			(x, _) when x > 3 => { return x; },
			(a, b) => { return a + b; }
		}
		return 0;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Match)
	if !ok {
		t.Fatalf("first stmt should be *ast.Match; got %T", prog.Funcs[0].Body.Stmts[0])
	}
	if len(m.Arms) != 3 {
		t.Fatalf("arms = %d, want 3", len(m.Arms))
	}
	a0 := m.Arms[0]
	if len(a0.TupleElems) != 2 || a0.TupleElems[0].Literal == nil || a0.TupleElems[1].Name != "y" {
		t.Errorf("arm 0 elems = %#v, want (literal, binder y)", a0.TupleElems)
	}
	a1 := m.Arms[1]
	if len(a1.TupleElems) != 2 || a1.TupleElems[0].Name != "x" || !a1.TupleElems[1].IsWildcard || a1.Guard == nil {
		t.Errorf("arm 1 elems = %#v (guard %v), want (binder x, _) with guard", a1.TupleElems, a1.Guard)
	}
	a2 := m.Arms[2]
	if len(a2.TupleElems) != 2 || a2.TupleElems[0].Name != "a" || a2.TupleElems[1].Name != "b" {
		t.Errorf("arm 2 elems = %#v, want (binder a, binder b)", a2.TupleElems)
	}
}

// TestTupleElemVariantPatternParses pins the variant sub-pattern element
// `(A(x), y)`: the payloads land on the element, not on the arm, and the
// payload-less spelling is the empty parens (a bare `A` stays a binder, which
// the checker rejects with E015). `mod.A(x)` carries the qualifier the same
// way an arm-position pattern does.
func TestTupleElemVariantPatternParses(t *testing.T) {
	prog, err := Parse(`enum Sh { A(i32), B, C(i32, i32) }
	function f(p: (Sh, i32)): i32 {
		match (p) {
			(A(x), y) => { return x + y; },
			(B(), y) => { return y; },
			(m.C(q, r), _) => { return q + r; },
			(z, y) => { return y; }
		}
		return 0;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Match)
	if !ok {
		t.Fatalf("first stmt should be *ast.Match; got %T", prog.Funcs[0].Body.Stmts[0])
	}
	e0 := m.Arms[0].TupleElems
	if len(e0) != 2 || e0[0].VariantName != "A" || len(e0[0].VariantBindings) != 1 ||
		e0[0].VariantBindings[0] != "x" || e0[0].Name != "" || e0[1].Name != "y" {
		t.Errorf("arm 0 elems = %#v, want (A(x), binder y)", e0)
	}
	e1 := m.Arms[1].TupleElems
	if len(e1) != 2 || e1[0].VariantName != "B" || len(e1[0].VariantBindings) != 0 {
		t.Errorf("arm 1 elems = %#v, want (B(), binder y)", e1)
	}
	e2 := m.Arms[2].TupleElems
	if len(e2) != 2 || e2[0].VariantModule != "m" || e2[0].VariantName != "C" ||
		len(e2[0].VariantBindings) != 2 || e2[0].VariantBindings[1] != "r" {
		t.Errorf("arm 2 elems = %#v, want (m.C(q, r), _)", e2)
	}
	// A bare name is still a binder — the spelling is what distinguishes them.
	e3 := m.Arms[3].TupleElems
	if len(e3) != 2 || e3[0].VariantName != "" || e3[0].Name != "z" {
		t.Errorf("arm 3 elems = %#v, want (binder z, binder y)", e3)
	}
}

// A variant element can fail to match, so the irrefutable binding sites have
// to refuse it the way they refuse a literal element. They share
// irrefutableDestructure, whose binder branch took `el.Name` unconditionally —
// which for a variant element is the empty string, so `let (A(x), y) = t;`
// bound a nameless local and silently discarded the pattern.
func TestTupleElemVariantIsRefutableAtBindingSites(t *testing.T) {
	const decls = "enum Sh { A(i32), B }\n"
	for name, src := range map[string]string{
		"let destructure": `function f(t: (Sh, i32)): i32 {
			let (A(x), y) = t;
			return y;
		}`,
		"parameter": `function f((A(x), y): (Sh, i32)): i32 { return y; }`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(decls + src)
			if err == nil {
				t.Fatal("want a refutable-pattern error, got none")
			}
			if !strings.Contains(err.Error(), "a variant tuple element can fail to match") {
				t.Errorf("want the refutable-element diagnostic, got: %v", err)
			}
		})
	}
}

// Tuple patterns parse in expression-form match arms too, and the
// remaining error shape holds: a singleton tuple pattern is a parse error
// at every nesting level. (Tuple or-patterns now parse — see
// TestMatchLiteralOrPatternParses; nested tuple elements parse — see
// TestNestedTuplePatternParses.)
func TestTupleMatchPatternErrors(t *testing.T) {
	if _, err := Parse(`function f(p: (i32, i32)): i32 {
		var v = match (p) { (0, y) => y, (a, b) => a };
		return v;
	}`); err != nil {
		t.Fatalf("expr-form tuple pattern should parse: %v", err)
	}
	cases := map[string]string{
		"singleton": `function f(p: (i32, i32)): i32 {
			match (p) { (a) => { return 1; } }
			return 0;
		}`,
		"singleton nested": `function f(p: ((i32, i32), i32)): i32 {
			match (p) { ((a), c) => { return 1; } }
			return 0;
		}`,
	}
	for name, src := range cases {
		if _, err := Parse(src); err == nil {
			t.Errorf("%s: expected parse error", name)
		}
	}
}

// A tuple element can be a nested tuple pattern, to any depth, mixing
// freely with the binder / `_` / literal / variant element forms.
func TestNestedTuplePatternParses(t *testing.T) {
	prog, err := Parse(`function f(p: ((i32, string), (i32, (i32, i32)))): i32 {
		match (p) {
			((1, "a"), (b, (c, _))) => { return b + c; },
			((x, _), (_, (y, z))) => { return x + y + z; }
		}
		return 0;
	}`)
	if err != nil {
		t.Fatalf("nested tuple pattern should parse: %v", err)
	}
	m, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Match)
	if !ok {
		t.Fatalf("first stmt should be *ast.Match; got %T", prog.Funcs[0].Body.Stmts[0])
	}
	if len(m.Arms) != 2 {
		t.Fatalf("want 2 arms, got %d", len(m.Arms))
	}
	first := m.Arms[0].TupleElems
	if len(first) != 2 {
		t.Fatalf("want 2 top-level elements, got %d", len(first))
	}
	if len(first[0].Nested) != 2 || first[0].Nested[0].Literal == nil {
		t.Fatalf("element 0 should nest a 2-element tuple starting with a literal: %+v", first[0])
	}
	// `(b, (c, _))` — the second level nests a third.
	deep := first[1].Nested
	if len(deep) != 2 || deep[0].Name != "b" {
		t.Fatalf("element 1 should nest `(b, …)`: %+v", first[1])
	}
	if len(deep[1].Nested) != 2 || deep[1].Nested[0].Name != "c" || !deep[1].Nested[1].IsWildcard {
		t.Fatalf("element 1 should nest `(c, _)` at depth 3: %+v", deep[1])
	}
}

// A tuple element's variant PAYLOAD slot is the same production the element
// itself is: a binder, `_`, a literal, a nested variant, or a tuple.
func TestTupleVariantPayloadSubPatternParses(t *testing.T) {
	prog, err := Parse(`enum Inner { Ok2(i32), Err2(i32) }
enum Outer { A(Inner), B }
enum Wrap { W((i32, i32)), Z(i32) }
function f(p: (Outer, Wrap)): i32 {
	match (p) {
		(A(Ok2(n)), W((0, b))) => { return n + b; },
		(A(x), Z(9)) => { return 9; },
		_ => { return 0; }
	}
	return 0;
}`)
	if err != nil {
		t.Fatalf("payload sub-pattern should parse: %v", err)
	}
	m, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Match)
	if !ok {
		t.Fatalf("first stmt should be *ast.Match; got %T", prog.Funcs[0].Body.Stmts[0])
	}
	first := m.Arms[0].TupleElems
	if len(first) != 2 {
		t.Fatalf("want 2 top-level elements, got %d", len(first))
	}
	// `A(Ok2(n))` — one payload slot, holding a variant sub-pattern, so the
	// slot's own binder name is empty.
	if len(first[0].VariantPayloads) != 1 || first[0].VariantPayloads[0] == nil {
		t.Fatalf("element 0 should carry a payload sub-pattern: %+v", first[0])
	}
	if got := first[0].VariantBindings[0]; got != "" {
		t.Fatalf("a sub-pattern slot binds nothing itself, got binder %q", got)
	}
	if sub := first[0].VariantPayloads[0]; sub.VariantName != "Ok2" || len(sub.VariantBindings) != 1 || sub.VariantBindings[0] != "n" {
		t.Fatalf("element 0's payload should be `Ok2(n)`: %+v", sub)
	}
	// `W((0, b))` — a TUPLE in the payload slot, with a literal inside it.
	tupSub := first[1].VariantPayloads[0]
	if tupSub == nil || len(tupSub.Nested) != 2 || tupSub.Nested[0].Literal == nil || tupSub.Nested[1].Name != "b" {
		t.Fatalf("element 1's payload should be `(0, b)`: %+v", first[1])
	}
	// A plain binder slot stays a binder — `A(x)` is not a sub-pattern.
	second := m.Arms[1].TupleElems
	if second[0].VariantBindings[0] != "x" {
		t.Fatalf("`A(x)` should still be a plain binder: %+v", second[0])
	}
	if second[0].VariantPayloads[0] != nil {
		t.Fatalf("`A(x)` should carry no sub-pattern: %+v", second[0])
	}
	// A LITERAL slot is a sub-pattern too — `Z(9)` tests, it does not bind.
	if lit := second[1].VariantPayloads[0]; lit == nil || lit.Literal == nil {
		t.Fatalf("`Z(9)` should carry a literal sub-pattern: %+v", second[1])
	}
}

// `let Variant(b) = … else { … };` continues to route to the let-else
// desugar — the tuple-destructure branch must not steal it. The
// statement that lands is the origin-tagged match, and the rest of the
// block is its success arm (that is where the bindings live).
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
	if len(stmts) != 1 {
		t.Fatalf("let-else should swallow the rest of the block; got %d stmts", len(stmts))
	}
	m, ok := stmts[0].(*ast.Match)
	if !ok {
		t.Fatalf("first stmt should be the desugared *ast.Match; got %T", stmts[0])
	}
	if m.Origin != ast.OriginLetElse {
		t.Errorf("Origin = %q, want %q", m.Origin, ast.OriginLetElse)
	}
	if len(m.Arms) != 2 || m.Arms[0].VariantName != "Some" || !m.Arms[1].IsWildcard {
		t.Fatalf("want a Some arm plus a wildcard else arm; got %+v", m.Arms)
	}
	if len(m.Arms[0].Body.Stmts) != 1 {
		t.Errorf("success arm should hold the rest of the block; got %+v", m.Arms[0].Body.Stmts)
	}
	if len(m.Arms[1].Body.Stmts) != 1 {
		t.Errorf("else arm should hold the else block; got %+v", m.Arms[1].Body.Stmts)
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

// `pub use "path".{a, b};` parses into Program.PubUses with the path
// and re-exported names. See docs/PRELUDE-TO-MODULES.md.
func TestPubUseParses(t *testing.T) {
	prog, err := Parse(`pub use "std/string".{split, trim};
pub use "./helpers".{add5};
function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(prog.PubUses) != 2 {
		t.Fatalf("expected 2 pub uses, got %d", len(prog.PubUses))
	}
	if prog.PubUses[0].Path != "std/string" ||
		len(prog.PubUses[0].Names) != 2 ||
		prog.PubUses[0].Names[0] != "split" || prog.PubUses[0].Names[1] != "trim" {
		t.Errorf("pub use[0] = %+v, want std/string {split, trim}", prog.PubUses[0])
	}
	if prog.PubUses[1].Path != "./helpers" || len(prog.PubUses[1].Names) != 1 || prog.PubUses[1].Names[0] != "add5" {
		t.Errorf("pub use[1] = %+v, want ./helpers {add5}", prog.PubUses[1])
	}
	// Empty name list is a parse error.
	if _, err := Parse(`pub use "x".{};`); err == nil {
		t.Error("`pub use \"x\".{};` with no names should be a parse error")
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

// A trait method may carry a `{ … }` default body; an abstract one
// still ends at `;`. The default body is retained on TraitMethod.Body
// for the checker to materialise per impl. See docs/TRAITS.md.
func TestTraitDefaultMethodParses(t *testing.T) {
	prog, err := Parse(`trait Greet {
    function name(self: Self): string;
    function greeting(self: Self): string { return "hi " + self.name(); }
}`)
	if err != nil {
		t.Fatalf("trait with default method should parse: %v", err)
	}
	td := prog.Traits[0]
	if len(td.Methods) != 2 {
		t.Fatalf("expected 2 methods, got %d", len(td.Methods))
	}
	if td.Methods[0].Body != nil {
		t.Errorf("abstract method `name` should have nil Body, got %+v", td.Methods[0].Body)
	}
	if td.Methods[1].Name != "greeting" || td.Methods[1].Body == nil {
		t.Errorf("default method `greeting` should retain its Body, got %+v", td.Methods[1])
	}
	if n := len(td.Methods[1].Body.Stmts); n != 1 {
		t.Errorf("default body should have 1 statement, got %d", n)
	}
}

// The path separator `::` in expression position parses to the same
// FieldAccess node as `.`, with PathSep set so the printer round-trips it.
// `Type::method(args)`, `mod::func()`, and `mod::CONST` all work. See #2700.
func TestPathSepParse(t *testing.T) {
	prog, err := Parse(`function main(): i32 {
    var a: i32 = Point::origin().x;
    var b: i32 = helpers::add5(10);
    var c: i32 = helpers::BONUS;
    return a + b + c;
}`)
	if err != nil {
		t.Fatal(err)
	}
	body := prog.Funcs[0].Body.Stmts
	fieldOf := func(e ast.Expr) *ast.FieldAccess {
		switch x := e.(type) {
		case *ast.FieldAccess:
			return x
		case *ast.Call:
			if fa, ok := x.Callee.(*ast.FieldAccess); ok {
				return fa
			}
		}
		return nil
	}
	// a = Point::origin().x → outer `.x` (dot) on Call(Point::origin()).
	ax := body[0].(*ast.Var).Init.(*ast.FieldAccess)
	if ax.PathSep {
		t.Errorf("`.x` should not be a path separator")
	}
	if origin := fieldOf(ax.Target); origin == nil || origin.Field != "origin" || !origin.PathSep {
		t.Errorf("Point::origin should be FieldAccess{Field:origin, PathSep:true}, got %#v", ax.Target)
	}
	if fa := fieldOf(body[1].(*ast.Var).Init); fa == nil || !fa.PathSep || fa.Field != "add5" {
		t.Errorf("helpers::add5 should be a PathSep FieldAccess, got %#v", body[1].(*ast.Var).Init)
	}
	if fa := fieldOf(body[2].(*ast.Var).Init); fa == nil || !fa.PathSep || fa.Field != "BONUS" {
		t.Errorf("helpers::BONUS should be a PathSep FieldAccess, got %#v", body[2].(*ast.Var).Init)
	}
}

// A type-parameter bound may carry trait type arguments
// (`function f[T: From[i32]]`), recorded on FuncDecl.BoundArgs parallel to
// Bounds. A non-generic bound (`U: Eq`) records no args. See docs/TRAITS.md.
func TestGenericTraitBoundParse(t *testing.T) {
	prog, err := Parse(`function f[T: From[i32] + Eq, U: Eq](a: T, b: U): i32 { return 0; }`)
	if err != nil {
		t.Fatal(err)
	}
	fn := prog.Funcs[0]
	if got := fn.Bounds["T"]; len(got) != 2 || got[0] != "From" || got[1] != "Eq" {
		t.Errorf("T bounds = %v, want [From Eq]", fn.Bounds["T"])
	}
	ta := fn.BoundArgs["T"]
	if len(ta) != 2 {
		t.Fatalf("T BoundArgs = %v, want 2 entries (one per bound)", ta)
	}
	if len(ta[0]) != 1 {
		t.Errorf("From bound args = %v, want [i32]", ta[0])
	}
	if _, ok := ta[0][0].(ast.NumberType); !ok {
		t.Errorf("From arg = %#v, want i32", ta[0][0])
	}
	if len(ta[1]) != 0 {
		t.Errorf("Eq bound should have no args, got %v", ta[1])
	}
	// A bound with no generic-trait args records nothing in BoundArgs.
	if _, ok := fn.BoundArgs["U"]; ok {
		t.Errorf("U (non-generic Eq bound) should not appear in BoundArgs")
	}
}

// A trait may declare type parameters (`trait From[T]`), recorded on
// TraitDecl.TypeParams, and an impl binds them via `impl From[i32] for T`,
// recorded on ImplDecl.TraitArgs. See docs/TRAITS.md.
func TestGenericTraitParse(t *testing.T) {
	prog, err := Parse(`trait From[T] { function from(v: T): Self; }
struct Celsius { deg: i32 }
impl From[i32] for Celsius { function from(v: i32): Self { return Celsius { deg: v }; } }`)
	if err != nil {
		t.Fatal(err)
	}
	tr := prog.Traits[0]
	if len(tr.TypeParams) != 1 || tr.TypeParams[0] != "T" {
		t.Errorf("trait TypeParams = %v, want [T]", tr.TypeParams)
	}
	impl := prog.Impls[0]
	if len(impl.TraitArgs) != 1 {
		t.Fatalf("impl TraitArgs = %v, want 1 arg", impl.TraitArgs)
	}
	if nt, ok := impl.TraitArgs[0].(ast.NumberType); !ok || nt.NormalWidth() != 32 {
		t.Errorf("impl TraitArgs[0] = %#v, want i32", impl.TraitArgs[0])
	}
}

// An inherent impl block (`impl Type { … }`, no `for Trait`, #2700) records an
// ImplDecl with an empty Trait and the named type as Type. A receiver-less
// function becomes an associated function (AssocType set); a `self`-taking one
// becomes a method (Receiver set).
func TestInherentImplParse(t *testing.T) {
	prog, err := Parse(`struct Pt { x: i32, y: i32 }
impl Pt {
	function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; }
	function sum(self: Self): i32 { return self.x + self.y; }
}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Impls) != 1 {
		t.Fatalf("expected 1 impl decl; got %d", len(prog.Impls))
	}
	impl := prog.Impls[0]
	if impl.Trait != "" {
		t.Errorf("inherent impl Trait = %q, want empty", impl.Trait)
	}
	st, ok := impl.Type.(ast.StructType)
	if !ok || st.Name != "Pt" {
		t.Errorf("inherent impl Type = %#v, want StructType{Pt}", impl.Type)
	}
	// Find the desugared functions among prog.Funcs.
	var assoc, method *ast.FuncDecl
	for _, fn := range prog.Funcs {
		if fn.AssocType == "Pt" && fn.Name == "make" {
			assoc = fn
		}
		if fn.Receiver != nil && fn.Name == "sum" {
			method = fn
		}
	}
	if assoc == nil {
		t.Error("associated function `make` not desugared with AssocType=Pt")
	}
	if method == nil {
		t.Error("method `sum` not desugared with a receiver")
	} else if _, ok := method.Receiver.Type.(ast.StructType); !ok {
		t.Errorf("method `sum` receiver type = %#v, want StructType{Pt}", method.Receiver.Type)
	}
}

// A generic inherent impl (`impl[T] Box[T] { … }`) records the impl type's
// generic args and carries the type params onto its functions.
func TestGenericInherentImplParse(t *testing.T) {
	prog, err := Parse(`struct Box[T] { v: T }
impl[T] Box[T] {
	function of(v: T): Box[T] { return Box { v: v }; }
}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Impls) != 1 {
		t.Fatalf("expected 1 impl decl; got %d", len(prog.Impls))
	}
	impl := prog.Impls[0]
	if impl.Trait != "" {
		t.Errorf("generic inherent impl Trait = %q, want empty", impl.Trait)
	}
	et, ok := impl.Type.(ast.EnumType)
	if !ok || et.Name != "Box" || len(et.Args) != 1 {
		t.Errorf("generic inherent impl Type = %#v, want Box[T]", impl.Type)
	}
}

// A trait may declare associated types (`type Item;`), an impl binds them
// (`type Item = i32;`), and signatures reference them as `Self::Item` /
// `T::Item` (parsed to ast.ProjType). See docs/ASSOCIATED-TYPES.md.
func TestAssociatedTypesParse(t *testing.T) {
	prog, err := Parse(`trait Iterator {
    type Item;
    function next(self: Self): Self::Item;
}
struct B { v: i32 }
impl Iterator for B {
    type Item = i32;
    function next(self: Self): Self::Item { return self.v; }
}
function first[I: Iterator](it: I): I::Item { return it.next(); }`)
	if err != nil {
		t.Fatal(err)
	}
	tr := prog.Traits[0]
	if len(tr.AssocTypes) != 1 || tr.AssocTypes[0] != "Item" {
		t.Errorf("trait AssocTypes = %v, want [Item]", tr.AssocTypes)
	}
	pj, ok := tr.Methods[0].Result.(ast.ProjType)
	if !ok || pj.Name != "Item" {
		t.Fatalf("next result = %T %v, want ProjType …::Item", tr.Methods[0].Result, tr.Methods[0].Result)
	}
	if _, ok := pj.Base.(ast.SelfType); !ok {
		t.Errorf("projection base = %T, want SelfType", pj.Base)
	}
	impl := prog.Impls[0]
	if impl.AssocTypeBindings == nil || impl.AssocTypeBindings["Item"] == nil {
		t.Fatalf("impl AssocTypeBindings missing Item: %v", impl.AssocTypeBindings)
	}
	var first *ast.FuncDecl
	for _, fn := range prog.Funcs {
		if fn.Name == "first" {
			first = fn
		}
	}
	if first == nil {
		t.Fatal("first not found")
	}
	if pj, ok := first.ReturnType.(ast.ProjType); !ok || pj.Name != "Item" {
		t.Errorf("first return = %T %v, want ProjType …::Item", first.ReturnType, first.ReturnType)
	}
}

// A trait may declare supertraits with `trait Ord: Eq + Hash { … }`;
// they're recorded on TraitDecl.Supertraits (qualifiers preserved). A
// trait with no `:` clause has an empty list. See docs/TRAITS.md.
func TestTraitSupertraitsParse(t *testing.T) {
	prog, err := Parse(`trait Eq { function eq(self: Self, other: Self): boolean; }
trait Hash { function hash(self: Self): i32; }
trait Ord: Eq + Hash { function lt(self: Self, other: Self): boolean; }`)
	if err != nil {
		t.Fatalf("trait with supertraits should parse: %v", err)
	}
	var ord, eq *ast.TraitDecl
	for _, td := range prog.Traits {
		switch td.Name {
		case "Ord":
			ord = td
		case "Eq":
			eq = td
		}
	}
	if ord == nil || eq == nil {
		t.Fatalf("expected Ord and Eq traits, got %d traits", len(prog.Traits))
	}
	if len(eq.Supertraits) != 0 {
		t.Errorf("Eq should have no supertraits, got %v", eq.Supertraits)
	}
	if len(ord.Supertraits) != 2 || ord.Supertraits[0] != "Eq" || ord.Supertraits[1] != "Hash" {
		t.Errorf("Ord supertraits = %v, want [Eq Hash]", ord.Supertraits)
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

// A parametric impl `impl[T: Bound] Trait for Box[T]` parses the
// leading type params onto the ImplDecl and propagates them (plus the
// bound) onto each desugared receiver-method, so the receiver-hoist
// registers the methods as generics. See docs/TRAITS.md (Phase 6).
// `dyn Trait` parses to a DynTraitType in type position, and
// `dyn Trait[]` wraps it in an array. See docs/DYN-TRAITS.md.
func TestDynTraitTypeParse(t *testing.T) {
	prog, err := Parse(`trait Shape { function area(self: Self): i32; }
function one(d: dyn Shape): i32 { return d.area(); }
function many(ds: dyn Shape[]): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("dyn type should parse: %v", err)
	}
	var one, many *ast.FuncDecl
	for _, fn := range prog.Funcs {
		switch fn.Name {
		case "one":
			one = fn
		case "many":
			many = fn
		}
	}
	if one == nil || many == nil {
		t.Fatal("functions not parsed")
	}
	dt, ok := one.Params[0].Type.(ast.DynTraitType)
	if !ok || len(dt.Traits) != 1 || dt.Traits[0] != "Shape" {
		t.Errorf("one param type = %#v, want dyn Shape", one.Params[0].Type)
	}
	at, ok := many.Params[0].Type.(ast.ArrayType)
	if !ok {
		t.Fatalf("many param type = %#v, want array", many.Params[0].Type)
	}
	if edt, ok := at.Elem.(ast.DynTraitType); !ok || len(edt.Traits) != 1 || edt.Traits[0] != "Shape" {
		t.Errorf("many element type = %#v, want dyn Shape", at.Elem)
	}
}

// `dyn Container[i32]` parses to a DynTraitType carrying the trait's
// pinned generic arguments (parallel Args). See docs/DYN-TRAITS.md.
func TestDynGenericTraitTypeParse(t *testing.T) {
	prog, err := Parse(`trait Container[T] { function get(self: Self): T; }
function take(d: dyn Container[i32]): i32 { return d.get(); }`)
	if err != nil {
		t.Fatalf("dyn generic type should parse: %v", err)
	}
	var take *ast.FuncDecl
	for _, fn := range prog.Funcs {
		if fn.Name == "take" {
			take = fn
		}
	}
	if take == nil {
		t.Fatal("function not parsed")
	}
	dt, ok := take.Params[0].Type.(ast.DynTraitType)
	if !ok || len(dt.Traits) != 1 || dt.Traits[0] != "Container" {
		t.Fatalf("take param type = %#v, want dyn Container[i32]", take.Params[0].Type)
	}
	args := dt.ArgsFor(0)
	if len(args) != 1 {
		t.Fatalf("Container args = %#v, want one (i32)", args)
	}
	if n, ok := args[0].(ast.NumberType); !ok || n.String() != "i32" {
		t.Errorf("Container arg[0] = %#v, want i32", args[0])
	}
	if got := dt.String(); got != "dyn Container[i32]" {
		t.Errorf("String() = %q, want %q", got, "dyn Container[i32]")
	}
}

// `dyn Producer[Item = i32]` parses to a DynTraitType carrying a pinned
// associated-type binding (AssocBindings), distinct from a positional generic
// argument. See docs/DYN-TRAITS.md.
func TestDynAssocTypeParse(t *testing.T) {
	prog, err := Parse(`trait Producer { type Item; function get(self: Self): Self::Item; }
function take(d: dyn Producer[Item = i32]): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("dyn assoc-pin type should parse: %v", err)
	}
	var take *ast.FuncDecl
	for _, fn := range prog.Funcs {
		if fn.Name == "take" {
			take = fn
		}
	}
	if take == nil {
		t.Fatal("function not parsed")
	}
	dt, ok := take.Params[0].Type.(ast.DynTraitType)
	if !ok || len(dt.Traits) != 1 || dt.Traits[0] != "Producer" {
		t.Fatalf("take param type = %#v, want dyn Producer[Item = i32]", take.Params[0].Type)
	}
	if len(dt.ArgsFor(0)) != 0 {
		t.Errorf("Producer should have no positional args, got %#v", dt.ArgsFor(0))
	}
	binds := dt.AssocFor(0)
	if len(binds) != 1 || binds[0].Name != "Item" {
		t.Fatalf("Producer assoc bindings = %#v, want one (Item)", binds)
	}
	if n, ok := binds[0].Type.(ast.NumberType); !ok || n.String() != "i32" {
		t.Errorf("Item binding = %#v, want i32", binds[0].Type)
	}
	if got := dt.String(); got != "dyn Producer[Item = i32]" {
		t.Errorf("String() = %q, want %q", got, "dyn Producer[Item = i32]")
	}
}

// `dyn A + B` parses to a DynTraitType carrying the SORTED + DEDUPED
// trait set (so `dyn A + B` ≡ `dyn B + A`), `dyn A + B + C` keeps all
// three, `dyn A+B[]` is an array of multi-trait objects, and a trailing
// `+` is a parse error. See docs/DYN-TRAITS.md.
func TestDynMultiTraitParse(t *testing.T) {
	prog, err := Parse(`trait A { function a(self: Self): i32; }
trait B { function b(self: Self): i32; }
trait C { function c(self: Self): i32; }
function f(d: dyn B + A): i32 { return 0; }
function g(d: dyn C + A + B): i32 { return 0; }
function h(ds: dyn A + B[]): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("multi-trait dyn should parse: %v", err)
	}
	byName := map[string]*ast.FuncDecl{}
	for _, fn := range prog.Funcs {
		byName[fn.Name] = fn
	}
	// `dyn B + A` normalises to the sorted set [A, B].
	dt, ok := byName["f"].Params[0].Type.(ast.DynTraitType)
	if !ok || len(dt.Traits) != 2 || dt.Traits[0] != "A" || dt.Traits[1] != "B" {
		t.Errorf("f param = %#v, want sorted dyn A + B", byName["f"].Params[0].Type)
	}
	if dt.String() != "dyn A + B" {
		t.Errorf("String() = %q, want %q", dt.String(), "dyn A + B")
	}
	// `dyn C + A + B` → [A, B, C].
	dt3, ok := byName["g"].Params[0].Type.(ast.DynTraitType)
	if !ok || len(dt3.Traits) != 3 || dt3.Traits[0] != "A" || dt3.Traits[1] != "B" || dt3.Traits[2] != "C" {
		t.Errorf("g param = %#v, want sorted dyn A + B + C", byName["g"].Params[0].Type)
	}
	// `dyn A + B[]` → array of dyn A + B.
	at, ok := byName["h"].Params[0].Type.(ast.ArrayType)
	if !ok {
		t.Fatalf("h param = %#v, want array", byName["h"].Params[0].Type)
	}
	if edt, ok := at.Elem.(ast.DynTraitType); !ok || len(edt.Traits) != 2 {
		t.Errorf("h element = %#v, want dyn A + B array", at.Elem)
	}
	// Order-insensitive Equal: `dyn A + B` ≡ `dyn B + A`.
	if !ast.Equal(ast.NewDynTraitType("A", "B"), ast.NewDynTraitType("B", "A")) {
		t.Errorf("dyn A + B should equal dyn B + A")
	}
	// Dedup: `dyn A + A` → single-element [A].
	if d := ast.NewDynTraitType("A", "A"); len(d.Traits) != 1 {
		t.Errorf("dyn A + A should dedup to [A], got %#v", d.Traits)
	}
	// Trailing `+` is a parse error.
	if _, err := Parse(`trait A { function a(self: Self): i32; }
function bad(d: dyn A +): i32 { return 0; }`); err == nil {
		t.Errorf("trailing `+` in dyn type should be a parse error")
	}
}

// `resource Name;` parses to a ResourceDecl (with its optional `@import`
// binding), and `own R` / `borrow R` parse to HandleType in type position.
// See docs/WIT-BRING-YOUR-OWN.md (P5).
func TestResourceHandleParse(t *testing.T) {
	prog, err := Parse(`@import("wasi:io/poll@0.2.0", "pollable")
resource Pollable;

@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): own Pollable;

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: borrow Pollable): boolean;

function take(hs: own Pollable[]): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("resource/handle syntax should parse: %v", err)
	}
	if len(prog.Resources) != 1 {
		t.Fatalf("got %d resources, want 1", len(prog.Resources))
	}
	rd := prog.Resources[0]
	if rd.Name != "Pollable" || rd.ImportIface != "wasi:io/poll@0.2.0" || rd.ImportWITName != "pollable" {
		t.Errorf("resource decl = %#v, want Pollable bound to wasi:io/poll@0.2.0/pollable", rd)
	}
	var subscribe, ready, take *ast.FuncDecl
	for _, fn := range prog.Funcs {
		switch fn.Name {
		case "subscribe":
			subscribe = fn
		case "ready":
			ready = fn
		case "take":
			take = fn
		}
	}
	if subscribe == nil || ready == nil || take == nil {
		t.Fatal("functions not parsed")
	}
	if h, ok := subscribe.ReturnType.(ast.HandleType); !ok || h.Resource != "Pollable" || h.Borrowed {
		t.Errorf("subscribe return = %#v, want own Pollable", subscribe.ReturnType)
	}
	if h, ok := ready.Params[0].Type.(ast.HandleType); !ok || h.Resource != "Pollable" || !h.Borrowed {
		t.Errorf("ready param = %#v, want borrow Pollable", ready.Params[0].Type)
	}
	// `own Pollable[]` is an array of owned handles (suffix `[]` binds after
	// the handle base type).
	at, ok := take.Params[0].Type.(ast.ArrayType)
	if !ok {
		t.Fatalf("take param = %#v, want array", take.Params[0].Type)
	}
	if h, ok := at.Elem.(ast.HandleType); !ok || h.Resource != "Pollable" || h.Borrowed {
		t.Errorf("take element = %#v, want own Pollable", at.Elem)
	}
}

// `own` / `borrow` stay usable as ordinary identifiers when not in the
// `own R` / `borrow R` handle-type position (contextual keywords).
func TestOwnBorrowStillIdentifiers(t *testing.T) {
	if _, err := Parse(`function f(own x: i32): i32 { return x; }`); err != nil {
		t.Fatalf("`own` param modifier should still parse: %v", err)
	}
	if _, err := Parse(`function g(borrow: i32): i32 { return borrow; }`); err != nil {
		t.Fatalf("`borrow` as a param name should still parse: %v", err)
	}
}

func TestParametricImplDecl(t *testing.T) {
	prog, err := Parse(`trait Display { function to_string(self: Self): string; }
struct Box[T] { v: T }
impl[T: Display] Display for Box[T] {
    function to_string(self: Self): string { return "b"; }
}`)
	if err != nil {
		t.Fatalf("parametric impl should parse: %v", err)
	}
	if len(prog.Impls) != 1 {
		t.Fatalf("expected 1 impl, got %d", len(prog.Impls))
	}
	impl := prog.Impls[0]
	if len(impl.TypeParams) != 1 || impl.TypeParams[0] != "T" {
		t.Errorf("impl.TypeParams = %v, want [T]", impl.TypeParams)
	}
	// The `for` type carries the type arg `Box[T]`. The parser
	// optimistically wraps `Name[…]` as an EnumType (it can't tell
	// structs from enums by name alone); the checker later rewrites
	// it to a StructType. Either way it must name Box with one arg.
	et, ok := impl.Type.(ast.EnumType)
	if !ok || et.Name != "Box" || len(et.Args) != 1 {
		t.Errorf("impl.Type = %s, want Box[T]", impl.Type)
	}
	// The desugared method inherits the type params + bound.
	var found *ast.FuncDecl
	for _, fn := range prog.Funcs {
		if fn.Name == "to_string" {
			found = fn
		}
	}
	if found == nil {
		t.Fatalf("impl method not appended to Program.Funcs")
	}
	if len(found.TypeParams) != 1 || found.TypeParams[0] != "T" {
		t.Errorf("method TypeParams = %v, want [T]", found.TypeParams)
	}
	if bs := found.Bounds["T"]; len(bs) != 1 || bs[0] != "Display" {
		t.Errorf("method Bounds[T] = %v, want [Display]", bs)
	}
}

// A method inside a parametric impl block must not declare its own
// leading type params — that nested-generic shape isn't supported.
func TestParametricImplMethodOwnTypeParamsRejected(t *testing.T) {
	if _, err := Parse(`trait T { function f(self: Self): void; }
impl[A] T for Box[A] { function [B] (self: Self) f(): void {} }`); err == nil {
		t.Error("impl-method with its own type params inside a parametric impl should be a parse error")
	}
}

// Error cases: trait method without a `self` first param, and `impl`
// missing the `for` clause.
func TestTraitImplParseErrors(t *testing.T) {
	// A trait method without a leading `self` is now an associated
	// function (e.g. a `Type.new()` constructor), not a parse error.
	if prog, err := Parse(`trait T { function f(x: i32): i32; }`); err != nil {
		t.Errorf("receiver-less trait method (associated function) should parse: %v", err)
	} else if m := prog.Traits[0].Methods[0]; !m.Assoc {
		t.Error("receiver-less trait method should be marked Assoc")
	}
	if _, err := Parse(`impl T Point { }`); err == nil {
		t.Error("`impl T Point` without `for` should be a parse error")
	}
	if _, err := Parse(`trait T { function f(self: Self): void; }
impl T for Self { function f(self: Self): void {} }`); err == nil {
		t.Error("`impl T for Self` should be a parse error")
	}
}

// A `var` declaration MUST carry an initializer — the grammar requires `=`
// after the (optionally-typed) binding, so an uninitialized declaration is a
// parse error, never an implicit zero. (`let` is the separate refutable
// let-else binding, not a plain declaration.) This is what closes
// the "uninitialized-var read is silently zero" footgun (#4409 part 1) at
// the source level: a program can't even spell an uninitialized local, so
// there is no read-before-init to diagnose. (Fall-off-end, #4409 part 2, is
// pinned separately by the checker's E052 tests.)
func TestVarDeclRequiresInitializer(t *testing.T) {
	rejected := []string{
		`function main(): i32 { var x: i32; return x; }`, // typed, no init
		`function main(): i32 { var x; return 0; }`,      // untyped, no init
	}
	for _, src := range rejected {
		if _, err := Parse(src); err == nil {
			t.Errorf("var declaration without initializer should be a parse error:\n%s", src)
		}
	}
	// The initialized forms still parse — the requirement is an initializer,
	// not a ban on the annotation.
	for _, src := range []string{
		`function main(): i32 { var x: i32 = 0; return x; }`,
		`function main(): i32 { var x = 0; return x; }`,
	} {
		if _, err := Parse(src); err != nil {
			t.Errorf("initialized var declaration should parse: %v\n%s", err, src)
		}
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

// `@import("iface", "wit-func")` on a body-less function records the WIT
// binding on FuncDecl.ImportIface / ImportWITName (bring-your-own WIT, P4 —
// docs/WIT-BRING-YOUR-OWN.md).
func TestImportAttributeParses(t *testing.T) {
	prog, err := Parse(`@import("wasi:random/random@0.2.0", "get-random-u64")
function get_random(): u64;`)
	if err != nil {
		t.Fatalf("@import should parse: %v", err)
	}
	if len(prog.Funcs) != 1 {
		t.Fatalf("expected one function, got %d", len(prog.Funcs))
	}
	fn := prog.Funcs[0]
	if fn.Body != nil {
		t.Errorf("@import function should be body-less, got Body=%v", fn.Body)
	}
	if fn.ImportIface != "wasi:random/random@0.2.0" {
		t.Errorf("ImportIface = %q, want wasi:random/random@0.2.0", fn.ImportIface)
	}
	if fn.ImportWITName != "get-random-u64" {
		t.Errorf("ImportWITName = %q, want get-random-u64", fn.ImportWITName)
	}

	// `@import` before `pub function` works too.
	prog2, err := Parse(`@import("a:b/c", "d") pub function f(): i32;`)
	if err != nil {
		t.Fatalf("@import pub function: %v", err)
	}
	if !prog2.Funcs[0].Public || prog2.Funcs[0].ImportIface != "a:b/c" {
		t.Errorf("expected public @import function, got %+v", prog2.Funcs[0])
	}
}

// `@export("iface", "wit-name")` on a function (with a body) records the WIT
// export binding on FuncDecl.ExportIface / ExportWITName (bring-your-own WIT,
// P6 — docs/WIT-BRING-YOUR-OWN.md).
func TestExportAttributeParses(t *testing.T) {
	prog, err := Parse(`@export("wasi:cli/run@0.2.0", "run")
function run(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("@export should parse: %v", err)
	}
	if len(prog.Funcs) != 1 {
		t.Fatalf("expected one function, got %d", len(prog.Funcs))
	}
	fn := prog.Funcs[0]
	if fn.Body == nil {
		t.Errorf("@export function should have a body")
	}
	if fn.ExportIface != "wasi:cli/run@0.2.0" || fn.ExportWITName != "run" {
		t.Errorf("export binding = {%q %q}, want {wasi:cli/run@0.2.0 run}", fn.ExportIface, fn.ExportWITName)
	}
	// `@export` before `pub function` works too.
	prog2, err := Parse(`@export("a:b/c", "d") pub function f(): i32 { return 1; }`)
	if err != nil {
		t.Fatalf("@export pub function: %v", err)
	}
	if !prog2.Funcs[0].Public || prog2.Funcs[0].ExportIface != "a:b/c" {
		t.Errorf("expected public @export function, got %+v", prog2.Funcs[0])
	}
}

// `@export` on a body-less function or a non-function is a parse error.
func TestExportAttributeErrors(t *testing.T) {
	if _, err := Parse(`@export("a:b/c", "d") function f(): i32;`); err == nil {
		t.Error("@export on a body-less function should be rejected")
	}
	if _, err := Parse(`@export("a:b/c", "d") struct S { x: i32 }`); err == nil {
		t.Error("@export on a struct should be rejected")
	}
}

// An @import function that carries a body, an @import on a non-function, and a
// body-less function without @import are all parse errors.
func TestImportAttributeErrors(t *testing.T) {
	// @import function with a body.
	if _, err := Parse(`@import("a:b/c", "d") function f(): i32 { return 0; }`); err == nil {
		t.Error("@import function with a body should be a parse error")
	}
	// @import on a struct.
	if _, err := Parse(`@import("a:b/c", "d") struct S { x: i32 }`); err == nil {
		t.Error("@import on a struct should be a parse error")
	}
	// Body-less function without @import.
	if _, err := Parse(`function f(): i32;`); err == nil {
		t.Error("body-less function without @import should be a parse error")
	}
	// @import needs two string arguments.
	if _, err := Parse(`@import("a:b/c") function f(): i32;`); err == nil {
		t.Error("@import with one argument should be a parse error")
	}
	if _, err := Parse(`@import(foo, bar) function f(): i32;`); err == nil {
		t.Error("@import with non-string arguments should be a parse error")
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

// `pub opaque struct` sets StructDecl.Opaque; `opaque` stays usable as
// an ordinary identifier elsewhere. See docs/TRAITS.md.
func TestOpaqueStructParses(t *testing.T) {
	prog, err := Parse(`pub opaque struct Email { addr: string }`)
	if err != nil {
		t.Fatalf("opaque struct should parse: %v", err)
	}
	if !prog.Structs[0].Opaque || !prog.Structs[0].Public {
		t.Errorf("expected public opaque struct, got %+v", prog.Structs[0])
	}
	// `opaque` not followed by `struct` is an ordinary identifier.
	p2, err := Parse(`function f(): i32 { var opaque: i32 = 5; return opaque; }`)
	if err != nil {
		t.Fatalf("`opaque` should be a valid identifier: %v", err)
	}
	if v, ok := p2.Funcs[0].Body.Stmts[0].(*ast.Var); !ok || v.Name != "opaque" {
		t.Fatalf("expected `var opaque`, got %T", p2.Funcs[0].Body.Stmts[0])
	}
}

// Arrow lambdas `(params): R => expr` desugar to an ast.Lambda whose body
// is `{ return expr; }`. Parens that hold a parameter list are an arrow
// lambda; ordinary grouping `(e)` and tuples `(e1, e2)` are unaffected.
// See #2701.
func TestArrowLambdaParse(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
  var g: (i32, i32) => i32 = (a: i32, b: i32): i32 => a + b;
  var h: () => i32 = (): i32 => 42;
  var grouped: i32 = (1 + 2) * 3;
  var pair: (i32, i32) = (4, 5);
  return g(grouped, pair.0) + h();
}`)
	if err != nil {
		t.Fatal(err)
	}
	stmts := prog.Funcs[0].Body.Stmts
	// `g` is a 2-param arrow lambda → Lambda{Body: {return a+b}}.
	g := stmts[0].(*ast.Var)
	lam, ok := g.Init.(*ast.Lambda)
	if !ok {
		t.Fatalf("g init should be a Lambda, got %T", g.Init)
	}
	if len(lam.Params) != 2 || lam.Params[0].Name != "a" || lam.Params[1].Name != "b" {
		t.Errorf("lambda params = %v, want [a b]", lam.Params)
	}
	if _, ok := lam.ReturnType.(ast.NumberType); !ok {
		t.Errorf("lambda return type = %T, want NumberType(i32)", lam.ReturnType)
	}
	if len(lam.Body.Stmts) != 1 {
		t.Fatalf("arrow body should be one stmt, got %d", len(lam.Body.Stmts))
	}
	if _, ok := lam.Body.Stmts[0].(*ast.Return); !ok {
		t.Errorf("arrow body stmt should be a Return, got %T", lam.Body.Stmts[0])
	}
	// `h` is a zero-param arrow lambda.
	if hl, ok := stmts[1].(*ast.Var).Init.(*ast.Lambda); !ok || len(hl.Params) != 0 {
		t.Errorf("h should be a zero-param Lambda, got %T", stmts[1].(*ast.Var).Init)
	}
	// Grouping is NOT a lambda.
	if _, ok := stmts[2].(*ast.Var).Init.(*ast.Lambda); ok {
		t.Errorf("(1 + 2) * 3 should not parse as a lambda")
	}
	// Tuple is NOT a lambda.
	if _, ok := stmts[3].(*ast.Var).Init.(*ast.TupleLit); !ok {
		t.Errorf("(4, 5) should parse as a TupleLit, got %T", stmts[3].(*ast.Var).Init)
	}
}

// Array.build desugars to a unique-local IIFE: a `var b: T[] = []`, the
// body with statement-position `b.append(x);` rewritten to `b = b.append(x)`,
// and a trailing `return b`. ArrayBuilder[T] is surface-only — it never
// survives the desugar. See docs/ARRAY-BUILDER-PLAN.md.
func TestArrayBuildDesugars(t *testing.T) {
	prog, err := Parse(`function f(): i32[] {
  return Array.build(function(b: ArrayBuilder[i32]): void {
    b.append(1);
    while (true) { b.append(2); }
  });
}`)
	if err != nil {
		t.Fatal(err)
	}
	ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	call, ok := ret.Value.(*ast.Call)
	if !ok {
		t.Fatalf("Array.build should desugar to an IIFE Call, got %T", ret.Value)
	}
	lam, ok := call.Callee.(*ast.Lambda)
	if !ok || len(call.Args) != 0 {
		t.Fatalf("IIFE callee should be a zero-arg Lambda, got %T with %d args", call.Callee, len(call.Args))
	}
	stmts := lam.Body.Stmts
	// First: `var b: i32[] = []`.
	v, ok := stmts[0].(*ast.Var)
	if !ok || v.Name != "b" {
		t.Fatalf("first stmt should be `var b`, got %T", stmts[0])
	}
	if at, ok := v.Type.(ast.ArrayType); !ok {
		t.Fatalf("b should be typed T[], got %v", v.Type)
	} else if _, ok := at.Elem.(ast.NumberType); !ok {
		t.Errorf("b's element type should be i32, got %v", at.Elem)
	}
	// Top-level `b.append(1);` rewritten to an assignment.
	es, ok := stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("second stmt should be ExprStmt, got %T", stmts[1])
	}
	if _, ok := es.Expr.(*ast.Assign); !ok {
		t.Fatalf("b.append should desugar to an Assign, got %T", es.Expr)
	}
	// Nested `b.append(2);` inside the while is rewritten too.
	w, ok := stmts[2].(*ast.While)
	if !ok {
		t.Fatalf("third stmt should be While, got %T", stmts[2])
	}
	inner := w.Body.(*ast.Block).Stmts[0].(*ast.ExprStmt)
	if _, ok := inner.Expr.(*ast.Assign); !ok {
		t.Errorf("nested b.append should desugar to an Assign, got %T", inner.Expr)
	}
	// Last: `return b`.
	rb, ok := stmts[len(stmts)-1].(*ast.Return)
	if !ok {
		t.Fatalf("last stmt should be `return b`, got %T", stmts[len(stmts)-1])
	}
	if id, ok := rb.Value.(*ast.Ident); !ok || id.Name != "b" {
		t.Errorf("should return the builder local `b`, got %v", rb.Value)
	}
}

// A malformed Array.build is a parse error (not a confusing "undefined
// Array" downstream).
func TestArrayBuildMalformed(t *testing.T) {
	if _, err := Parse(`function f(): i32[] { return Array.build(42); }`); err == nil {
		t.Error("Array.build with a non-lambda argument should be a parse error")
	}
}

// Map.build desugars to a unique-local IIFE: `var b: Map[K,V] = map_new(8)`,
// the body with `b.insert(k,v);` rewritten to `b = b.insert(k,v)`, and a
// trailing `return b`. See docs/ARRAY-BUILDER-PLAN.md.
func TestMapBuildDesugars(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
  var m = Map.build(function(b: MapBuilder[i32, i32]): void {
    b.insert(1, 2);
  });
  return 0;
}`)
	if err != nil {
		t.Fatal(err)
	}
	v := prog.Funcs[0].Body.Stmts[0].(*ast.Var)
	call, ok := v.Init.(*ast.Call)
	if !ok {
		t.Fatalf("Map.build should desugar to an IIFE Call, got %T", v.Init)
	}
	lam, ok := call.Callee.(*ast.Lambda)
	if !ok || len(call.Args) != 0 {
		t.Fatalf("IIFE callee should be a zero-arg Lambda, got %T", call.Callee)
	}
	// First: `var b: Map[i32,i32] = map_new(8)`.
	bv, ok := lam.Body.Stmts[0].(*ast.Var)
	if !ok || bv.Name != "b" {
		t.Fatalf("first stmt should be `var b`, got %T", lam.Body.Stmts[0])
	}
	if st, ok := bv.Type.(ast.StructType); !ok || st.Name != "Map" || len(st.Args) != 2 {
		t.Fatalf("b should be typed Map[K,V], got %v", bv.Type)
	}
	mc, ok := bv.Init.(*ast.Call)
	if !ok {
		t.Fatalf("b init should be a map_new call, got %T", bv.Init)
	}
	if id, ok := mc.Callee.(*ast.Ident); !ok || id.Name != "map_new" {
		t.Errorf("b should init from map_new, got %v", mc.Callee)
	}
	// `b.insert(1,2);` rewritten to an assignment.
	es, ok := lam.Body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("second stmt should be ExprStmt, got %T", lam.Body.Stmts[1])
	}
	if _, ok := es.Expr.(*ast.Assign); !ok {
		t.Errorf("b.insert should desugar to an Assign, got %T", es.Expr)
	}
}

// TestDefaultParam covers parsing default parameter values
// (`function f(a: i32, b: i32 = 128)`) and the rule that a required
// parameter may not follow a defaulted one.
func TestDefaultParam(t *testing.T) {
	prog, err := Parse(`function f(a: i32, b: i32 = 128): i32 { return a + b; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fn := prog.Funcs[0]
	if len(fn.Params) != 2 {
		t.Fatalf("want 2 params, got %d", len(fn.Params))
	}
	if fn.Params[0].Default != nil {
		t.Errorf("param a should have no default")
	}
	def, ok := fn.Params[1].Default.(*ast.NumberLit)
	if !ok || def.Value != 128 {
		t.Errorf("param b default should be NumberLit 128, got %#v", fn.Params[1].Default)
	}
}

func TestDefaultParamRequiredAfterOptional(t *testing.T) {
	_, err := Parse(`function f(a: i32 = 1, b: i32): i32 { return a + b; }`)
	if err == nil {
		t.Fatal("expected an error: required param after a defaulted one")
	}
}

// TestNamedArgs covers parsing named call arguments (`f(a, b = 2)`): ArgNames
// is parallel to Args, "" for positional, and nil when all positional.
func TestNamedArgs(t *testing.T) {
	prog, err := Parse(`function f(a: i32, b: i32, c: i32): i32 { return a; } function main(): i32 { return f(1, c = 3, b = 2); }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	body := prog.Funcs[1].Body
	ret := body.Stmts[0].(*ast.Return)
	call := ret.Value.(*ast.Call)
	if len(call.Args) != 3 {
		t.Fatalf("want 3 args, got %d", len(call.Args))
	}
	if len(call.ArgNames) != 3 || call.ArgNames[0] != "" || call.ArgNames[1] != "c" || call.ArgNames[2] != "b" {
		t.Errorf("ArgNames = %#v, want [\"\", \"c\", \"b\"]", call.ArgNames)
	}

	// All-positional call leaves ArgNames nil.
	prog2, err := Parse(`function f(a: i32): i32 { return a; } function main(): i32 { return f(1); }`)
	if err != nil {
		t.Fatalf("parse2: %v", err)
	}
	call2 := prog2.Funcs[1].Body.Stmts[0].(*ast.Return).Value.(*ast.Call)
	if call2.ArgNames != nil {
		t.Errorf("all-positional call should have nil ArgNames, got %#v", call2.ArgNames)
	}
}

// `e as? T` parses to a DowncastExpr (the fallible dyn-Trait downcast),
// while plain `e as T` stays a CastExpr (numeric cast / ascription).
// docs/DYN-TRAITS.md §9.
func TestParseDowncastVsCast(t *testing.T) {
	prog, err := Parse(`function f(s: dyn Shape): i32 { var c: Option[Circle] = s as? Circle; return 0; }`)
	if err != nil {
		t.Fatalf("parse downcast: %v", err)
	}
	v := prog.Funcs[0].Body.Stmts[0].(*ast.Var)
	dc, ok := v.Init.(*ast.DowncastExpr)
	if !ok {
		t.Fatalf("expected *ast.DowncastExpr, got %T", v.Init)
	}
	if _, ok := dc.Inner.(*ast.Ident); !ok {
		t.Errorf("downcast inner = %T, want *ast.Ident", dc.Inner)
	}
	if st, ok := dc.Target.(ast.StructType); !ok || st.Name != "Circle" {
		t.Errorf("downcast target = %v, want Circle struct", dc.Target)
	}

	// Plain `as` stays a CastExpr.
	prog2, err := Parse(`function g(n: i32): i64 { return n as i64; }`)
	if err != nil {
		t.Fatalf("parse cast: %v", err)
	}
	ret := prog2.Funcs[0].Body.Stmts[0].(*ast.Return)
	if _, ok := ret.Value.(*ast.CastExpr); !ok {
		t.Fatalf("expected *ast.CastExpr, got %T", ret.Value)
	}
	if _, ok := ret.Value.(*ast.DowncastExpr); ok {
		t.Fatalf("plain `as` must not parse to DowncastExpr")
	}
}

// #4521: a bare `{ … }` in a general value position parses as a block-expr,
// while `Ident { … }` stays a struct literal and a loop/if HEADER's trailing
// `{` still opens the body (not a block-expr) — the disambiguation contract.
func TestParseValuePositionBlockExpr(t *testing.T) {
	// Bare `{ stmts; tail }` on a `var` RHS → *ast.BlockExpr.
	prog, err := Parse(`function f(): i32 { var n: i32 = { var k = 3; k * 2 }; return n; }`)
	if err != nil {
		t.Fatalf("parse value-position block: %v", err)
	}
	v := prog.Funcs[0].Body.Stmts[0].(*ast.Var)
	be, ok := v.Init.(*ast.BlockExpr)
	if !ok {
		t.Fatalf("value-position `{ … }` init = %T, want *ast.BlockExpr", v.Init)
	}
	if len(be.Stmts) != 1 || be.Tail == nil {
		t.Errorf("block-expr shape = %d stmts, tail=%v; want 1 stmt + a tail", len(be.Stmts), be.Tail != nil)
	}

	// `Ident { … }` in the same position stays a struct literal.
	prog2, err := Parse(`struct P { x: i32 } function g(): i32 { var p: P = P { x: 1 }; return p.x; }`)
	if err != nil {
		t.Fatalf("parse struct lit: %v", err)
	}
	if _, ok := prog2.Funcs[0].Body.Stmts[0].(*ast.Var).Init.(*ast.StructLit); !ok {
		t.Fatalf("`P { x: 1 }` must stay a *ast.StructLit, got %T",
			prog2.Funcs[0].Body.Stmts[0].(*ast.Var).Init)
	}

	// A `for … in expr {` header's `{` opens the loop body, not a block-expr —
	// the noStructLit gate keeps the new rule out of loop/if headers.
	if _, err := Parse(`function h(xs: i32[]): i32 { var s = 0; for x in xs { s = s + x; } return s; }`); err != nil {
		t.Fatalf("for-in header must still parse (its `{` is the body): %v", err)
	}
	// Single-expr `{ e }` stays a bare expression (branch-form passthrough).
	prog3, err := Parse(`function k(): i32 { var n: i32 = { 7 }; return n; }`)
	if err != nil {
		t.Fatalf("parse single-expr block: %v", err)
	}
	if _, ok := prog3.Funcs[0].Body.Stmts[0].(*ast.Var).Init.(*ast.BlockExpr); ok {
		t.Errorf("`{ 7 }` must collapse to the bare expr `7`, not a BlockExpr")
	}
}

// #4522: an else-LESS `if` inside a block body parses as a control-flow
// STATEMENT (*ast.If among the block's Stmts), while an `if` WITH `else` stays
// a value expression — the disambiguation that lets `{ if (c) { return } tail }`
// carry control flow without breaking `{ …; if (c) { a } else { b } }`.
func TestParseControlFlowInBlockExpr(t *testing.T) {
	// else-less `if` in a value block → a leading *ast.If statement + a tail.
	prog, err := Parse(`function f(e: i32): i32 { var x: i32 = { if (e < 0) { return 9; } e + 1 }; return x; }`)
	if err != nil {
		t.Fatalf("parse control-flow block: %v", err)
	}
	be, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Var).Init.(*ast.BlockExpr)
	if !ok {
		t.Fatalf("init = %T, want *ast.BlockExpr", prog.Funcs[0].Body.Stmts[0].(*ast.Var).Init)
	}
	if len(be.Stmts) != 1 {
		t.Fatalf("block-expr Stmts = %d, want 1 (the if-statement)", len(be.Stmts))
	}
	if _, ok := be.Stmts[0].(*ast.If); !ok {
		t.Errorf("leading stmt = %T, want *ast.If (else-less if is control flow)", be.Stmts[0])
	}
	if be.Tail == nil {
		t.Errorf("block-expr tail is nil, want the `e + 1` value")
	}

	// An `if` WITH `else` as the tail stays a value expression (an *ast.IfExpr
	// tail, not a statement) — unchanged behaviour.
	prog2, err := Parse(`function g(c: boolean): i32 { var x: i32 = if (c) { 1 } else { 2 }; return x; }`)
	if err != nil {
		t.Fatalf("parse if-expr branch: %v", err)
	}
	if _, ok := prog2.Funcs[0].Body.Stmts[0].(*ast.Var).Init.(*ast.IfExpr); !ok {
		t.Errorf("if-with-else init = %T, want *ast.IfExpr",
			prog2.Funcs[0].Body.Stmts[0].(*ast.Var).Init)
	}
}

// TestParseStrViewType: `str` (#4813) is the contextual borrowed-string view
// type — an Ident claimed only in TYPE position, deliberately not a lexer
// keyword, so `str` locals and `.str(...)` methods in expression position
// keep working.
func TestParseStrViewType(t *testing.T) {
	prog, err := Parse(`function f(v: str, vs: str[]): str {
    var w: str = v;
    return w;
}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fn := prog.Funcs[0]
	if _, ok := fn.Params[0].Type.(ast.StrType); !ok {
		t.Errorf("param v: got %T, want ast.StrType", fn.Params[0].Type)
	}
	arr, ok := fn.Params[1].Type.(ast.ArrayType)
	if !ok {
		t.Fatalf("param vs: got %T, want ArrayType", fn.Params[1].Type)
	}
	if _, ok := arr.Elem.(ast.StrType); !ok {
		t.Errorf("param vs elem: got %T, want ast.StrType", arr.Elem)
	}
	if _, ok := fn.ReturnType.(ast.StrType); !ok {
		t.Errorf("return: got %T, want ast.StrType", fn.ReturnType)
	}
	if got := fn.ReturnType.String(); got != "str" {
		t.Errorf("String() = %q, want \"str\"", got)
	}

	// Expression position is untouched: `str` as a plain local identifier.
	if _, err := Parse(`function g(): i32 { var str: i32 = 2; return str; }`); err != nil {
		t.Errorf("`str` as an identifier: %v", err)
	}
	// A module-qualified struct reference `str.Foo` stays on the nominal
	// path (the `str.` guard) rather than parsing as the view type.
	prog2, err := Parse(`function h(x: str.Foo): void {}`)
	if err != nil {
		t.Fatalf("str.Foo: %v", err)
	}
	st, ok := prog2.Funcs[0].Params[0].Type.(ast.StructType)
	if !ok || st.Name != "str.Foo" {
		t.Errorf("str.Foo: got %#v, want StructType{Name: \"str.Foo\"}", prog2.Funcs[0].Params[0].Type)
	}
}

// `@` bindings work over literal and range sub-patterns, not just over
// variant / struct / tuple ones — `n @ 1..10` is the form the unified
// pattern grammar is specified around. Only `_` stays rejected: a wildcard
// arm is the unconditional default at every downstream stage, with no
// pattern to bind alongside.
func TestParseAtBindingOnLiteralAndRange(t *testing.T) {
	for _, tc := range []struct{ name, src, wantAt string }{
		{"range_exclusive", `function f(n: i32): i32 { match (n) { k @ 1..10 => { return k; }, _ => { return 0; } } }`, "k"},
		{"range_inclusive", `function f(n: i32): i32 { match (n) { k @ 1..=10 => { return k; }, _ => { return 0; } } }`, "k"},
		{"plain_literal", `function f(n: i32): i32 { match (n) { k @ 7 => { return k; }, _ => { return 0; } } }`, "k"},
		{"negative_literal", `function f(n: i32): i32 { match (n) { k @ -7 => { return k; }, _ => { return 0; } } }`, "k"},
		{"string_literal", `function f(s: string): i32 { match (s) { k @ "yes" => { return 1; }, _ => { return 0; } } }`, "k"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			m, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Match)
			if !ok {
				t.Fatalf("stmt 0 is %T, want *ast.Match", prog.Funcs[0].Body.Stmts[0])
			}
			if got := m.Arms[0].AtBinding; got != tc.wantAt {
				t.Errorf("AtBinding = %q, want %q", got, tc.wantAt)
			}
			if m.Arms[0].Literal == nil {
				t.Errorf("arm lost its literal low bound")
			}
		})
	}

	// `_` is the one sub-pattern an `@` cannot carry.
	if _, err := Parse(`function f(n: i32): i32 { match (n) { k @ _ => { return k; } } }`); err == nil {
		t.Errorf("`k @ _` parsed, want a diagnostic")
	} else if !strings.Contains(err.Error(), "`_` pattern") {
		t.Errorf("`k @ _` diagnostic = %v, want it to name the `_` pattern", err)
	}
}

// A negative literal pattern is an ast.Unary wrapping a positive literal,
// not a NumberLit with a negated Value: a negative Value is how the tree
// spells an unsigned magnitude above i64::MAX, and folding the sign in made
// the two indistinguishable downstream.
func TestParseNegativeLiteralPatternShape(t *testing.T) {
	prog, err := Parse(`function f(n: i32): i32 { match (n) { -1 => { return 7; }, _ => { return 0; } } }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	m := prog.Funcs[0].Body.Stmts[0].(*ast.Match)
	un, ok := m.Arms[0].Literal.(*ast.Unary)
	if !ok {
		t.Fatalf("literal is %T, want *ast.Unary", m.Arms[0].Literal)
	}
	if un.Op != "-" {
		t.Errorf("Op = %q, want \"-\"", un.Op)
	}
	num, ok := un.Operand.(*ast.NumberLit)
	if !ok {
		t.Fatalf("operand is %T, want *ast.NumberLit", un.Operand)
	}
	if num.Value != 1 {
		t.Errorf("operand Value = %d, want 1 (the sign belongs to the Unary)", num.Value)
	}
}

// The parser has no types, so a bare name in a payload slot is a binder and
// only the empty-paren spelling nests. This pins the split the checker's
// binder-vs-payload-less-variant diagnostic (E015) is built on: `Wrap(Err2)`
// stays one flat arm binding the whole payload, while `Wrap(Err2())` becomes
// the merged arm whose body re-matches the payload.
func TestBarePayloadSlotNameStaysABinder(t *testing.T) {
	armOf := func(t *testing.T, src string) *ast.MatchArm {
		t.Helper()
		prog, err := Parse(`enum Res { Ok2(i32), Err2 }
enum Nest { Wrap(Res), Bare }
function f(w: Nest): i32 {
  ` + src + `
  return 0;
}`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		m, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Match)
		if !ok {
			t.Fatalf("first stmt should be *ast.Match; got %T", prog.Funcs[0].Body.Stmts[0])
		}
		return m.Arms[0]
	}

	bare := armOf(t, `match (w) { Wrap(Err2) => { return 9; }, _ => { return 1; } }`)
	if len(bare.Bindings) != 1 || bare.Bindings[0] != "Err2" {
		t.Errorf("bare form Bindings = %v, want [Err2] (a binder)", bare.Bindings)
	}
	if len(bare.Body.Stmts) != 1 {
		t.Errorf("bare form body should be the arm body unchanged, got %d stmts", len(bare.Body.Stmts))
	} else if _, nested := bare.Body.Stmts[0].(*ast.Match); nested {
		t.Error("bare form must not desugar to an inner match — the parser cannot tell binder from variant")
	}

	paren := armOf(t, `match (w) { Wrap(Err2()) => { return 9; }, _ => { return 1; } }`)
	if len(paren.Bindings) != 1 || paren.Bindings[0] == "Err2" {
		t.Errorf("paren form Bindings = %v, want a single synthetic temp", paren.Bindings)
	}
	if len(paren.Body.Stmts) != 1 {
		t.Fatalf("paren form body should be the inner match alone, got %d stmts", len(paren.Body.Stmts))
	}
	inner, ok := paren.Body.Stmts[0].(*ast.Match)
	if !ok {
		t.Fatalf("paren form body should re-match the payload; got %T", paren.Body.Stmts[0])
	}
	if inner.Arms[0].VariantName != "Err2" || len(inner.Arms[0].Bindings) != 0 {
		t.Errorf("inner arm = %q/%v, want the payload-less variant Err2", inner.Arms[0].VariantName, inner.Arms[0].Bindings)
	}
}
