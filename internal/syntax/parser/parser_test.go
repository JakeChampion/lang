package parser

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

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
	if c := codeOf(`function main(): i32 { let x: f64 = 1e309; return 0; }`); c != "P002" {
		t.Errorf("out-of-range float literal: code = %q, want P002", c)
	}
	if c := codeOf(`function main(): i32 { let x: f32 = 1e400f32; return 0; }`); c != "P002" {
		t.Errorf("out-of-range suffixed float literal: code = %q, want P002", c)
	}
	for _, src := range []string{
		`function main(): i32 { let x: f64 = 1.7976931348623157e308; return 0; }`,
		`function main(): i32 { let x: f64 = 1.7976931348623158e308; return 0; }`,
		`function main(): i32 { let x: f64 = 1e-400; return 0; }`,
		`function main(): i32 { let x: f64 = 5e-324; return 0; }`,
		`function main(): i32 { let x: f32 = 3.5e38; return 0; }`,
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

func TestParseDeferBlockForm(t *testing.T) {
	prog, err := Parse(`function f(): Result[i32, i32] {
		let x: i32 = 0;
		defer { x = x + 1; }
		errdefer { x = x + 2; }
		return Ok(x);
	}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	stmts := prog.Funcs[0].Body.Stmts
	for i, want := range []bool{false, true} {
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
	check(0, true)
	check(1, false)

	if _, err := Parse(`function main(): i32 { let assert: i32 = 5; return assert; }`); err != nil {
		t.Errorf("`assert` as an identifier should still parse: %v", err)
	}
}

func TestKeywordModuleQualifier(t *testing.T) {
	prog, err := Parse(`function f(): string { return string.repeat_char(120 as char, 4); }`)
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
	if _, err := Parse(`function f(): i32 { return string; }`); err == nil {
		t.Error("bare `string` in expression position should still be a parse error")
	}
}

func TestPrecedence(t *testing.T) {
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

func TestBitwiseLooserThanCompare(t *testing.T) {
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
	prog, err := Parse("function f(): i32 { let a: i32[] = [1,2,3]; return a[1]; }")
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

func TestForProducesForNode(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		for (let i = 0; i < 3; i = i + 1) { i; }
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

func TestForEachParsesUnlowered(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		let sum: i32 = 0;
		for x in [1, 2, 3] {
			sum = sum + x;
		}
		return sum;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	fe, ok := prog.Funcs[0].Body.Stmts[1].(*ast.ForEach)
	if !ok {
		t.Fatalf("foreach should parse to *ast.ForEach, got %T", prog.Funcs[0].Body.Stmts[1])
	}
	if fe.Var != "x" {
		t.Errorf("binder = %q, want x", fe.Var)
	}
	if _, ok := fe.Iter.(*ast.ArrayLit); !ok {
		t.Errorf("iterand = %T, want the array literal", fe.Iter)
	}
}

func TestForEachOverStructFieldParses(t *testing.T) {
	prog, err := Parse(`struct Bag { items: i32[] }
	function f(): i32 {
		let b = Bag { items: [1, 2, 3] };
		let sum: i32 = 0;
		for x in b.items {
			sum = sum + x;
		}
		return sum;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	fe, ok := prog.Funcs[0].Body.Stmts[2].(*ast.ForEach)
	if !ok {
		t.Fatalf("foreach over a struct field should parse to *ast.ForEach, got %T", prog.Funcs[0].Body.Stmts[2])
	}
	if _, ok := fe.Iter.(*ast.FieldAccess); !ok {
		t.Fatalf("foreach iter should be a FieldAccess (b.items), got %T", fe.Iter)
	}
}

func TestForInRangeDesugars(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		let sum: i32 = 0;
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

func TestForInInclusiveRangeDesugars(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		let sum: i32 = 0;
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
	if len(blk.Stmts) != 4 {
		t.Fatalf("expected 4 inner stmts (hi / i / go / for), got %d", len(blk.Stmts))
	}
	hi, ok1 := blk.Stmts[0].(*ast.Var)
	iv, ok2 := blk.Stmts[1].(*ast.Var)
	gv, ok3 := blk.Stmts[2].(*ast.Var)
	if !ok1 || !ok2 || !ok3 || iv.Name != "i" {
		t.Fatalf("expected `let hi; let i; let go`, got %T %T %T", blk.Stmts[0], blk.Stmts[1], blk.Stmts[2])
	}
	if b, ok := gv.Init.(*ast.Binary); !ok || b.Op != "<=" {
		t.Errorf("go flag should start as `i <= hi` (empty when LOW > HIGH), got %T", gv.Init)
	}
	loop, ok := blk.Stmts[3].(*ast.For)
	if !ok {
		t.Fatalf("fourth stmt should be a For (so continue runs the step), got %T", blk.Stmts[3])
	}
	if loop.Init != nil {
		t.Errorf("the loop let is declared before the go flag, not in Init; got %T", loop.Init)
	}
	if id, ok := loop.Cond.(*ast.Ident); !ok || id.Name != gv.Name {
		t.Errorf("inclusive range loop cond should be the go flag %q, got %T", gv.Name, loop.Cond)
	}
	step, ok := loop.Step.(*ast.Block)
	if !ok || len(step.Stmts) != 2 {
		t.Fatalf("step should be `{ go = i != hi; i = i + 1; }`, got %T", loop.Step)
	}
	clear := step.Stmts[0].(*ast.ExprStmt).Expr.(*ast.Assign)
	cmp, ok := clear.Value.(*ast.Binary)
	if tgt, _ := clear.Target.(*ast.Ident); tgt == nil || tgt.Name != gv.Name || !ok || cmp.Op != "!=" {
		t.Errorf("step must clear the flag with `go = i != hi` before the increment")
	}
	if r, ok := cmp.Right.(*ast.Ident); !ok || r.Name != hi.Name {
		t.Errorf("the flag compares against the HIGH binding %q", hi.Name)
	}
}

func TestLoopParsesToCanonicalLoopNode(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		let i: i32 = 0;
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

func TestForEachNestedHasUniqueIDs(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		let s: i32 = 0;
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
	outer, ok := prog.Funcs[0].Body.Stmts[1].(*ast.ForEach)
	if !ok {
		t.Fatalf("outer loop should be *ast.ForEach, got %T", prog.Funcs[0].Body.Stmts[1])
	}
	inner, ok := outer.Body.(*ast.Block).Stmts[0].(*ast.ForEach)
	if !ok {
		t.Fatalf("inner loop should be *ast.ForEach, got %T", outer.Body.(*ast.Block).Stmts[0])
	}
	if outer.ID == inner.ID {
		t.Errorf("nested loops share ID %d", outer.ID)
	}
}

func TestForEachPatternHeaderParses(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		names  []string
		nested []int
	}{
		{name: "map pair", src: "for (k, v) in m { sum = sum + k + v; }", names: []string{"k", "v"}},
		{name: "trailing comma", src: "for (k, v,) in m { sum = sum + k + v; }", names: []string{"k", "v"}},
		{name: "arity three", src: "for (a, b, c) in xs { sum = sum + a + b + c; }", names: []string{"a", "b", "c"}},
		{name: "nested element", src: "for ((a, b), c) in xs { sum = sum + a + b + c; }", nested: []int{0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := Parse("function f(): i32 {\n let sum: i32 = 0;\n" + tc.src + "\n return sum;\n}")
			if err != nil {
				t.Fatal(err)
			}
			fe, ok := prog.Funcs[0].Body.Stmts[1].(*ast.ForEach)
			if !ok {
				t.Fatalf("a pattern header should stay an un-lowered ForEach for the checker, got %T", prog.Funcs[0].Body.Stmts[1])
			}
			if fe.Pattern == nil {
				t.Fatalf("a destructuring header must carry its Pattern")
			}
			if fe.Pattern.Init != nil {
				t.Errorf("Pattern.Init is the lowering's to fill, got %#v", fe.Pattern.Init)
			}
			for _, i := range tc.nested {
				if i >= len(fe.Pattern.Nested) || fe.Pattern.Nested[i] == nil {
					t.Fatalf("element %d should carry a nested pattern, got %#v", i, fe.Pattern.Nested)
				}
			}
			if tc.names != nil && !reflect.DeepEqual(fe.Pattern.Names, tc.names) {
				t.Errorf("pattern names = %v, want %v", fe.Pattern.Names, tc.names)
			}
		})
	}
}

func TestForHeaderParenDisambiguation(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		let sum: i32 = 0;
		for (let i: i32 = 0; i < 3; i = i + 1) { sum = sum + i; }
		return sum;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := prog.Funcs[0].Body.Stmts[1].(*ast.For); !ok {
		t.Errorf("C-style for should still parse to *ast.For, got %T", prog.Funcs[0].Body.Stmts[1])
	}
	_, err = Parse(`function f(): i32 { for (x) in xs { } return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "tuple pattern needs at least 2 elements") {
		t.Errorf("for (x) in xs should report the shared tuple-pattern error, got %v", err)
	}
}

func TestForWithExprInit(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		let i = 0;
		for (i = 0; i < 3; i = i + 1) {}
		return 0;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	_ = prog
}

func TestForEmptyInitAndStep(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		let i = 0;
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

func TestRecoversAndReportsMultiplePerStatement(t *testing.T) {
	src := `function f(): i32 {
		let x = ;
		let y = 1 +;
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

func TestRefusedTypeSpellings(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`function f(x: float): i32 { return 0; }`, "written `f64`"},
		{`function f(): Option[float] { return None; }`, "written `f64`"},
		{`function f(): Result[(), string] { return Ok(()); }`, "written `void`"},
		{`function f(u: ()): i32 { return 0; }`, "written `void`"},
	} {
		_, err := Parse(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want an error saying %q", tc.src, err, tc.want)
		}
	}
	prog, err := Parse(`function g(v: float.Vec): void { let u = (); }`)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := prog.Funcs[0].Params[0].Type.(ast.StructType)
	if !ok || st.Name != "float.Vec" {
		t.Errorf("qualified type = %#v, want StructType{Name:\"float.Vec\"}", prog.Funcs[0].Params[0].Type)
	}
}

func TestCompoundAssignDesugars(t *testing.T) {
	prog, err := Parse(`function f(): i32 { let x: i32 = 1; x += 2; return x; }`)
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

func TestCompoundAssignFieldDesugars(t *testing.T) {
	prog, err := Parse(`struct A { v: i32 } function f(): i32 { let a: A = A { v: 1 }; a.v += 2; return a.v; }`)
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

func hasParseCode(err error, code string) bool {
	var batch diag.Errors
	if errors.As(err, &batch) {
		for _, e := range batch {
			if hasParseCode(e, code) {
				return true
			}
		}
		return false
	}
	var coded diag.Coded
	return errors.As(err, &coded) && coded.Code() == code
}

func TestNonLvalueAssignTargetsRejected(t *testing.T) {
	for _, src := range []string{
		`function f(): i32 { 1 = 2; return 0; }`,
		`function g(): i32 { return 0; } function f(): i32 { g() = 2; return 0; }`,
		`function f(): i32 { "s" = "t"; return 0; }`,
		`function f(): i32 { let a: i32 = 1; a.b.c() = 2; return a; }`,
	} {
		_, err := Parse(src)
		if err == nil {
			t.Errorf("expected P003 for %s, got no error", src)
			continue
		}
		if !hasParseCode(err, "P003") {
			t.Errorf("want P003 for %s, got %T: %v", src, err, err)
		}
	}
}

func TestRefusalsNameTheirConstruct(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"let x = [1\n2];", `expected "]" to close this array literal`},
		{"let x = g(1\n2);", `expected ")" to close this call`},
		{"let a = [1]; let x = a[0;", `expected "]" to close this index`},
		{"let a = [1]; let x = a[0:1;", `expected "]" to close this slice`},
		{"let a = 1; a.;", `expected a field or method name after "."`},
		{"let x 1;", `expected "=" after the name in this declaration`},
		{"let x: i32 1;", `expected "=" after the name in this declaration`},
		{"let p = P { 1 };", "expected a field name in this struct literal"},
		{"let p = P { a 1 };", `expected ":" after the field name in this struct literal`},
		{"let p = P { a: 1 a: 2 };", `expected "}" to close this struct literal`},
		{"let p = P { a: 1 }; let q = P { ...p a: 2 };", `expected "}" to close this struct update`},
	} {
		src := "struct P { a: i32 }\nfunction g(a: i32, b: i32): i32 { return a; }\nfunction f(): i32 {\n" + tc.body + "\nreturn 0;\n}"
		_, err := Parse(src)
		if err == nil || !hasParseCode(err, "P001") || !strings.Contains(err.Error(), ": "+tc.want) {
			t.Errorf("%s: want P001 %q, got %v", tc.body, tc.want, err)
		}
	}
	if _, err := Parse("function f(): i32 { let Some(x) 5 else { return 1; }; return 0; }"); err == nil || !strings.Contains(err.Error(), `expected "=", got "5"`) {
		t.Errorf(`a refutable pattern keeps "expected \"=\", got": %v`, err)
	}
}

func TestAssignmentIsAStatement(t *testing.T) {
	for _, src := range []string{
		`function f(): i32 { let r = 0; let a = r = 5; return a; }`,
		`function f(): i32 { let r = 0; while ((r = r + 1) < 3) { } return r; }`,
		`function f(): i32 { let r = 0; return r += 1; }`,
		`function f(): i32 { let r = 0; let g = (x: i32) => r = x; return r; }`,
	} {
		if _, err := Parse(src); err == nil {
			t.Errorf("assignment in expression position parsed: %s", src)
		}
	}
	for _, src := range []string{
		`function f(): i32 { let r = 0; let s = 0; r = s = 3; return r; }`,
		`function f(): i32 { let r = 0; for (r = 1; r < 3; r += 1) { } return r; }`,
		`function f(): i32 { let r = 0; defer r = 9; return r; }`,
	} {
		if _, err := Parse(src); err != nil {
			t.Errorf("assignment statement refused: %s: %v", src, err)
		}
	}
}

func TestCompoundAssignmentStandsAlone(t *testing.T) {
	for _, tc := range []struct {
		stmt string
		col  int
	}{
		{"a += b = 1;", 10},
		{"a = b += 5;", 9},
		{"a += b += 5;", 10},
		{"a = b = c += 2;", 13},
	} {
		src := "function f(): i32 {\n  let a = 0; let b = 0; let c = 0;\n  " + tc.stmt + "\n  return a;\n}"
		_, err := Parse(src)
		if err == nil || !hasParseCode(err, "P001") || !strings.Contains(err.Error(), chainedCompoundMessage) {
			t.Errorf("%s: want P001 %q, got %v", tc.stmt, chainedCompoundMessage, err)
			continue
		}
		if at := "at 3:" + strconv.Itoa(tc.col) + ":"; !strings.Contains(err.Error(), at) {
			t.Errorf("%s: want the error %s, got %v", tc.stmt, at, err)
		}
	}
	if _, err := Parse("function f(): i32 { let a = 0; let b = 0; a = b = 1; a += 2; return a; }"); err != nil {
		t.Errorf("a plain chain and a lone compound assignment: %v", err)
	}
}

func TestGenericUnionDeclParses(t *testing.T) {
	prog, err := Parse(`struct Leaf[T] { v: T }
struct Node[T] { v: T }
struct Lit { v: i32 }
type Tree[T] = Leaf[T] | Node[T] | Lit;
function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Unions) != 1 {
		t.Fatalf("unions = %d, want 1", len(prog.Unions))
	}
	ud := prog.Unions[0]
	if got := ud.TypeParams; len(got) != 1 || got[0] != "T" {
		t.Errorf("TypeParams = %v, want [T]", got)
	}
	if len(ud.Members) != 3 {
		t.Fatalf("members = %d, want 3", len(ud.Members))
	}
	for i, want := range []string{"Leaf", "Node", "Lit"} {
		if ud.Members[i].Name != want {
			t.Errorf("member %d = %q, want %q", i, ud.Members[i].Name, want)
		}
	}
	for _, i := range []int{0, 1} {
		args := ud.Members[i].Args
		if len(args) != 1 {
			t.Fatalf("member %q args = %v, want one", ud.Members[i].Name, args)
		}
		st, ok := args[0].(ast.StructType)
		if !ok || st.Name != "T" {
			t.Errorf("member %q arg = %#v, want the type parameter T", ud.Members[i].Name, args[0])
		}
	}
	if got := ud.Members[2].Args; len(got) != 0 {
		t.Errorf("plain member Lit carries args %v, want none", got)
	}
}

func TestUnionMemberConcreteTypeArgParses(t *testing.T) {
	prog, err := Parse(`struct Leaf[T] { v: T }
struct Lit { v: i32 }
type Tree = Leaf[i32] | Lit;
function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatal(err)
	}
	ud := prog.Unions[0]
	if len(ud.TypeParams) != 0 {
		t.Errorf("TypeParams = %v, want none", ud.TypeParams)
	}
	args := ud.Members[0].Args
	if len(args) != 1 {
		t.Fatalf("Leaf args = %v, want one", args)
	}
	if _, ok := args[0].(ast.NumberType); !ok {
		t.Errorf("Leaf arg = %#v, want i32", args[0])
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

func TestTryOpParses(t *testing.T) {
	prog, err := Parse(`function f(m: Map[i32, i32]): Option[i32] {
		let v: i32 = m.get(7)?;
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

func TestRadixLiteralValue(t *testing.T) {
	cases := map[string]int64{
		"0o755":   493,
		"0O17":    15,
		"0o0":     0,
		"0b1010":  10,
		"0B11111": 31,
	}
	for src, wantVal := range cases {
		prog, err := Parse(`function f(): i32 { return ` + src + `; }`)
		if err != nil {
			t.Errorf("%s: parse: %v", src, err)
			continue
		}
		lit, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value.(*ast.NumberLit)
		if !ok {
			t.Errorf("%s: not a *NumberLit", src)
			continue
		}
		if lit.Value != wantVal || lit.Raw != src {
			t.Errorf("%s: Value = %d, Raw = %q, want %d, %q", src, lit.Value, lit.Raw, wantVal, src)
		}
	}
}

func TestDigitSeparatorLiteralValue(t *testing.T) {
	cases := map[string]int64{
		"1_000_000":   1000000,
		"0xff_ff":     65535,
		"0o7_7":       63,
		"0b1010_0101": 165,
	}
	for src, wantVal := range cases {
		prog, err := Parse(`function f(): i32 { return ` + src + `; }`)
		if err != nil {
			t.Errorf("%s: parse: %v", src, err)
			continue
		}
		lit, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value.(*ast.NumberLit)
		if !ok {
			t.Errorf("%s: not a *NumberLit", src)
			continue
		}
		if lit.Value != wantVal || lit.Raw != src {
			t.Errorf("%s: Value = %d, Raw = %q, want %d, %q", src, lit.Value, lit.Raw, wantVal, src)
		}
	}
	prog, err := Parse(`function f(): f64 { return 1_000.25e0_1; }`)
	if err != nil {
		t.Fatalf("float: parse: %v", err)
	}
	if lit := prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value.(*ast.FloatLit); lit.Value != 10002.5 || lit.Raw != "1_000.25e0_1" {
		t.Errorf("float: Value = %v, Raw = %q", lit.Value, lit.Raw)
	}
}

func TestNumericLiteralSuffixes(t *testing.T) {
	type want struct {
		isFloat    bool
		width      int
		isUnsigned bool
	}
	cases := map[string]want{
		"42i32":   {false, 32, false},
		"42i64":   {false, 64, false},
		"42u8":    {false, 8, true},
		"42u32":   {false, 32, true},
		"42u64":   {false, 64, true},
		"42usize": {false, ast.WidthPtr, true},
		"1.5f32":  {true, 32, false},
		"1.5f64":  {true, 64, false},
		"42f64":   {true, 64, false},
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

func TestBlockExprIfBranch(t *testing.T) {
	prog, err := Parse(`function f(e: i32): i32 {
		return if (e > 0) { let k = e + 1; k } else { 0 };
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
	if _, ok := ie.Else.(*ast.BlockExpr); ok {
		t.Errorf("else `{ 0 }` (single expr, no stmts) should stay a bare expr, got *BlockExpr")
	}
}

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

func TestBlockExprNoTail(t *testing.T) {
	prog, err := Parse(`function f(b: boolean): i32 {
		return if (b) { let k = 1; } else { 0 };
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

func TestBlockExprElseIfChain(t *testing.T) {
	prog, err := Parse(`function f(n: i32): i32 {
		return if (n == 1) { let a = 10; a } else if (n == 2) { 20 } else { 30 };
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

func TestBlockExprMatchArm(t *testing.T) {
	prog, err := Parse(`function f(tag: i32): i32 {
		return match (tag) {
			0 => { let s = tag + 5; s },
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
			let p: P = P { x: 5 };
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

func TestStructUpdateLitParses(t *testing.T) {
	prog, err := Parse(`struct P { x: i32, y: i32 }
		function main(): i32 {
			let a: P = P { x: 1, y: 2 };
			let b: P = P { ...a, y: 9 };
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

func TestStructUpdateLitPureCopyParses(t *testing.T) {
	prog, err := Parse(`struct P { x: i32, y: i32 }
		function main(): i32 {
			let a: P = P { x: 1, y: 2 };
			let b: P = P { ...a };
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
		{"spread", `Box[i32] { ...b, val: 1 }`, "Box", []string{"i32"}, true},
		{"no args", `Box { val: 1 }`, "Box", nil, false},
		{"named arg", `Box[Loc] { val: l }`, "Box", []string{"Loc"}, false},
		{"qualified named arg", `Box[m.Loc] { val: l }`, "Box", []string{"m.Loc"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := Parse(`function main(): i32 { let v = ` + tc.init + `; return 0; }`)
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

func TestStructLitTypeArgsDoNotClaimIndexing(t *testing.T) {
	prog, err := Parse(`function main(): i32 {
			let xs = [1, 2, 3];
			let n = 0;
			while (xs[0] > 0) { n = n + 1; }
			let y = xs[1];
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

func TestCallTypeArgsShapes(t *testing.T) {
	cases := []struct {
		call string
		args []string
	}{
		{`f[i32](a)`, []string{"i32"}},
		{`f[Box, Loc](a)`, []string{"Box", "Loc"}},
		{`f[Pair[i32]](a)`, []string{"Pair[i32]"}},
		{`f[Box[]](a)`, []string{"Box[]"}},
		{`f[Box, (i32, Loc)](a)`, []string{"Box", "(i32, Loc)"}},
		{`f[(i32, Loc)](a)`, []string{"(i32, Loc)"}},
		{`f[(i32) => i32](a)`, []string{"(i32) => i32"}},
		{`f[(Box) => Loc, i32](a)`, []string{"(Box) => Loc", "i32"}},
		{`f[((Box) => Loc) => Box](a)`, []string{"((Box) => Loc) => Box"}},
		{`f[Box](a)`, nil},
		{`fs[(i)](a)`, nil},
		{`fs[(i - 1)](a)`, nil},
		{`fs[(m, k)](a)`, nil},
		{`fs[i](a)`, nil},
		{`fs[g[i32](1)](a)`, nil},
		{`fs[m.k](a)`, nil},
	}
	for _, tc := range cases {
		prog, err := Parse(`function main(): i32 { let v = ` + tc.call + `; return 0; }`)
		if err != nil {
			t.Fatalf("parse %s: %v", tc.call, err)
		}
		c, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Var).Init.(*ast.Call)
		if !ok {
			t.Fatalf("%s: init should be a Call", tc.call)
		}
		if tc.args == nil {
			if _, isIndex := c.Callee.(*ast.Index); !isIndex || len(c.TypeArgs) != 0 {
				t.Errorf("%s: callee should stay an Index, got %T with type args %v", tc.call, c.Callee, c.TypeArgs)
			}
			continue
		}
		if !c.TypeArgsWritten || len(c.TypeArgs) != len(tc.args) {
			t.Fatalf("%s: got type args %v (written %v), want %v", tc.call, c.TypeArgs, c.TypeArgsWritten, tc.args)
		}
		for i, want := range tc.args {
			if got := c.TypeArgs[i].String(); got != want {
				t.Errorf("%s: TypeArgs[%d] = %s, want %s", tc.call, i, got, want)
			}
		}
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
	prog, err := Parse(`struct P { x: i32 }
		function describe(p: P): i32 { return p.x; }`)
	if err != nil {
		t.Fatal(err)
	}
	if prog.Funcs[0].Receiver != nil {
		t.Error("regular function got mistakenly parsed as a method")
	}
}

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
	if _, err := Parse(`pub(crate) function f(): i32 { return 1; }`); err == nil {
		t.Error("`pub(crate)` should be a parse error")
	}
}

func TestPubBeforeUnsupportedKindIsError(t *testing.T) {
	_, err := Parse(`pub let x: i32 = 1;`)
	if err == nil {
		t.Fatal("expected parse error for `pub var`")
	}
	if !strings.Contains(err.Error(), "pub") {
		t.Errorf("error should mention `pub`; got %v", err)
	}
}

func TestDecimalLiteralOverflowIsFlagged(t *testing.T) {
	const lit = "99999999999999999999999999"
	prog, err := Parse(`function main(): i64 { return ` + lit + `; }`)
	if err != nil {
		t.Fatalf("an out-of-range literal is the checker's to refuse, so it parses: %v", err)
	}
	var got *ast.NumberLit
	ast.Walk(prog.Funcs[0].Body, func(n ast.Node) bool {
		if l, ok := n.(*ast.NumberLit); ok {
			got = l
		}
		return true
	})
	if got == nil || !got.ExceedsU64 || got.Raw != lit {
		t.Fatalf("literal = %+v, want ExceedsU64 with %s in Raw", got, lit)
	}
}

func TestLargeU64DecimalLiteralParses(t *testing.T) {
	if _, err := Parse(`function main(): u64 { return 18446744073709551615u64; }`); err != nil {
		t.Fatalf("u64-max literal should parse: %v", err)
	}
}

func TestLargeU64HexLiteralParses(t *testing.T) {
	prog, err := Parse(`function main(): u64 { return 0xd807aa98a3030242 as u64; }`)
	if err != nil {
		t.Fatalf("u64 hex literal should parse: %v", err)
	}
	var lit *ast.NumberLit
	ast.Walk(prog.Funcs[0].Body, func(n ast.Node) bool {
		if l, ok := n.(*ast.NumberLit); ok {
			lit = l
		}
		return true
	})
	if lit == nil || uint64(lit.Value) != 0xd807aa98a3030242 {
		t.Fatalf("hex literal value = %v, want the 0xd807aa98a3030242 bit pattern", lit)
	}
	prog, err = Parse(`function main(): u64 { return 0x1ffffffffffffffff as u64; }`)
	if err != nil {
		t.Fatalf("17-digit hex literal should parse flagged: %v", err)
	}
	lit = nil
	ast.Walk(prog.Funcs[0].Body, func(n ast.Node) bool {
		if l, ok := n.(*ast.NumberLit); ok {
			lit = l
		}
		return true
	})
	if lit == nil || !lit.ExceedsU64 || lit.Raw != "0x1ffffffffffffffff" {
		t.Fatalf("17-digit hex literal = %+v, want ExceedsU64 with its spelling in Raw", lit)
	}
}

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

func TestEnumVariantEmptyParensRejected(t *testing.T) {
	_, err := Parse(`enum C { R(), B }`)
	if err == nil || !strings.Contains(err.Error(), `expected a type, got ")"`) {
		t.Fatalf("`R()` in an enum declaration: got %v, want expected type", err)
	}
}

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
	if _, err := Parse(`enum E { V {} }`); err == nil {
		t.Error("`V {}` (empty named-field body) should be a parse error")
	}
}

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

func TestStructArmsMayProjectDifferentFieldLists(t *testing.T) {
	f, err := Parse(`struct P { x: i32, y: i32 }
function f(p: P): i32 {
    match (p) {
        P { x: 0, y } => { return y; },
        P { x } => { return x; },
        _ => { return 0; },
    }
}`)
	if err != nil {
		t.Fatalf("should parse: %v", err)
	}
	m, ok := f.Funcs[0].Body.Stmts[0].(*ast.Match)
	if !ok {
		t.Fatalf("first stmt should be *ast.Match; got %T", f.Funcs[0].Body.Stmts[0])
	}
	if len(m.Arms) != 3 {
		t.Fatalf("want 3 flat arms, got %d", len(m.Arms))
	}
	if got := m.Arms[0].FieldNames; len(got) != 2 {
		t.Errorf("arm 0 should project both fields, got %v", got)
	}
	if got := m.Arms[1].FieldNames; len(got) != 1 || got[0] != "x" {
		t.Errorf("arm 1 should project only x, got %v", got)
	}
	if m.Arms[0].Payloads == nil || m.Arms[0].Payloads[0] == nil {
		t.Error("arm 0 field x should carry a literal sub-pattern")
	}
}

func TestMatchStmtParses(t *testing.T) {
	prog, err := Parse(`enum E { A, B(i32) }
function f(): i32 {
	let e: E = A;
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
	if len(m.Arms) != 4 {
		t.Fatalf("expected 4 desugared arms; got %d", len(m.Arms))
	}
	wantVariant := []string{"A", "D", "B", "C"}
	for i, w := range wantVariant {
		if m.Arms[i].VariantName != w {
			t.Errorf("arm %d variant = %q, want %q", i, m.Arms[i].VariantName, w)
		}
	}
	if len(m.Arms[2].Bindings) != 1 || m.Arms[2].Bindings[0] != "n" {
		t.Errorf("arm B should bind `n`; got %+v", m.Arms[2].Bindings)
	}
	if len(m.Arms[3].Bindings) != 1 || m.Arms[3].Bindings[0] != "n" {
		t.Errorf("arm C should bind `n`; got %+v", m.Arms[3].Bindings)
	}
	if m.Arms[0].Body == m.Arms[1].Body {
		t.Error("or-pattern alternatives must not share the same *Block pointer")
	}
	if m.Arms[2].Body == m.Arms[3].Body {
		t.Error("or-pattern alternatives must not share the same *Block pointer")
	}
}

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

func TestStreamTypeParse(t *testing.T) {
	prog, err := Parse(`function f(s: Stream[u8]): i32 { return 0; }
function g(): Stream[string] { return None; }
function h(xs: Stream[u8][]): i32 { return 0; }`)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := prog.Funcs[0].Params[0].Type.(ast.StreamType)
	if !ok {
		t.Fatalf("f's param should be Stream[u8]; got %+v", prog.Funcs[0].Params[0].Type)
	}
	if n, ok := st.Elem.(ast.NumberType); !ok || n.NormalWidth() != 8 {
		t.Errorf("stream element should be u8; got %+v", st.Elem)
	}
	if st.String() != "Stream[u8]" {
		t.Errorf("StreamType.String() = %q, want Stream[u8]", st.String())
	}
	rt, ok := prog.Funcs[1].ReturnType.(ast.StreamType)
	if !ok {
		t.Fatalf("g's return type should be Stream[string]; got %+v", prog.Funcs[1].ReturnType)
	}
	if _, ok := rt.Elem.(ast.StringType); !ok {
		t.Errorf("stream element should be string; got %+v", rt.Elem)
	}
	at, ok := prog.Funcs[2].Params[0].Type.(ast.ArrayType)
	if !ok {
		t.Fatalf("h's param should be an array; got %+v", prog.Funcs[2].Params[0].Type)
	}
	if _, ok := at.Elem.(ast.StreamType); !ok {
		t.Errorf("h's array element should be Stream[u8]; got %+v", at.Elem)
	}
}

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

func TestNestedTupleDestructureSingletonError(t *testing.T) {
	if _, err := Parse(`function f(t: (i32, (i32, i32))): i32 { let (a, (b)) = t; return a; }`); err == nil {
		t.Error("expected parse error for a singleton nested position")
	}
}

func TestTupleDestructureSingleNameError(t *testing.T) {
	_, err := Parse(`function f(): i32 { let (a) = (1, 2); return a; }`)
	if err == nil {
		t.Fatal("expected parse error for single-name destructure")
	}
}

func TestStructDestructureParses(t *testing.T) {
	prog, err := Parse(`struct Point { x: i32, y: i32, z: i32 }
	function f(): i32 {
		let p: Point = Point { x: 1, y: 2, z: 3 };
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

func TestStructDestructureVarKeyword(t *testing.T) {
	prog, err := Parse(`struct Point { x: i32, y: i32 }
	function f(): i32 {
		let p: Point = Point { x: 1, y: 2 };
		let Point { x, y } = p;
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

func TestTupleParamDestructureLambdas(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
		let g = ((x, y): (i32, i32)): i32 => { return x * y; };
		let h = ((lo, hi): (i32, i32)) => hi - lo;
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
	e3 := m.Arms[3].TupleElems
	if len(e3) != 2 || e3[0].VariantName != "" || e3[0].Name != "z" {
		t.Errorf("arm 3 elems = %#v, want (binder z, binder y)", e3)
	}
}

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

func TestTupleMatchPatternErrors(t *testing.T) {
	if _, err := Parse(`function f(p: (i32, i32)): i32 {
		let v = match (p) { (0, y) => y, (a, b) => a };
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
	deep := first[1].Nested
	if len(deep) != 2 || deep[0].Name != "b" {
		t.Fatalf("element 1 should nest `(b, …)`: %+v", first[1])
	}
	if len(deep[1].Nested) != 2 || deep[1].Nested[0].Name != "c" || !deep[1].Nested[1].IsWildcard {
		t.Fatalf("element 1 should nest `(c, _)` at depth 3: %+v", deep[1])
	}
}

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
	if len(first[0].VariantPayloads) != 1 || first[0].VariantPayloads[0] == nil {
		t.Fatalf("element 0 should carry a payload sub-pattern: %+v", first[0])
	}
	if got := first[0].VariantBindings[0]; got != "" {
		t.Fatalf("a sub-pattern slot binds nothing itself, got binder %q", got)
	}
	if sub := first[0].VariantPayloads[0]; sub.VariantName != "Ok2" || len(sub.VariantBindings) != 1 || sub.VariantBindings[0] != "n" {
		t.Fatalf("element 0's payload should be `Ok2(n)`: %+v", sub)
	}
	tupSub := first[1].VariantPayloads[0]
	if tupSub == nil || len(tupSub.Nested) != 2 || tupSub.Nested[0].Literal == nil || tupSub.Nested[1].Name != "b" {
		t.Fatalf("element 1's payload should be `(0, b)`: %+v", first[1])
	}
	second := m.Arms[1].TupleElems
	if second[0].VariantBindings[0] != "x" {
		t.Fatalf("`A(x)` should still be a plain binder: %+v", second[0])
	}
	if second[0].VariantPayloads[0] != nil {
		t.Fatalf("`A(x)` should carry no sub-pattern: %+v", second[0])
	}
	if lit := second[1].VariantPayloads[0]; lit == nil || lit.Literal == nil {
		t.Fatalf("`Z(9)` should carry a literal sub-pattern: %+v", second[1])
	}
}

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

func TestIfLetRecordsChainedElse(t *testing.T) {
	for _, tc := range []struct {
		name, els string
		chained   bool
	}{
		{"else-if-let", `else if let Some(y) = o { return y; }`, true},
		{"else-if", `else if (true) { return 2; }`, true},
		{"else-block-if-let", `else { if let Some(y) = o { return y; } }`, false},
		{"else-block", `else { return 3; }`, false},
		{"no-else", ``, false},
	} {
		prog, err := Parse(`enum Option[T] { Some(T), None }
function f(o: Option[i32]): i32 {
	if let Some(x) = o { return x; } ` + tc.els + `
	return 0;
}`)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		m, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Match)
		if !ok {
			t.Fatalf("%s: first stmt should be the desugared *ast.Match; got %T", tc.name, prog.Funcs[0].Body.Stmts[0])
		}
		if m.ElseChained != tc.chained {
			t.Errorf("%s: ElseChained = %v, want %v", tc.name, m.ElseChained, tc.chained)
		}
	}
}

func TestValueIfRecordsChainedElse(t *testing.T) {
	for _, tc := range []struct {
		name, expr string
		chained    bool
	}{
		{"if-else-if", `if (n > 1) { 1 } else if (n > 0) { 2 } else { 3 }`, true},
		{"if-else-if-let", `if (n > 1) { 1 } else if let Some(y) = o { y } else { 3 }`, true},
		{"if-else-block-if", `if (n > 1) { 1 } else { if (n > 0) { 2 } else { 3 } }`, false},
		{"if-else", `if (n > 1) { 1 } else { 3 }`, false},
		{"if-let-else-if-let", `if let Some(x) = o { x } else if let Some(y) = o { y } else { 0 }`, true},
		{"if-let-else-if", `if let Some(x) = o { x } else if (n > 0) { 2 } else { 0 }`, true},
		{"if-let-else-block-if-let", `if let Some(x) = o { x } else { if let Some(y) = o { y } else { 0 } }`, false},
		{"if-let-else", `if let Some(x) = o { x } else { 0 }`, false},
	} {
		prog, err := Parse(`enum Option[T] { Some(T), None }
function f(o: Option[i32], n: i32): i32 {
	let r: i32 = ` + tc.expr + `;
	return r;
}`)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		v, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Var)
		if !ok {
			t.Fatalf("%s: first stmt should be *ast.Var; got %T", tc.name, prog.Funcs[0].Body.Stmts[0])
		}
		var got bool
		switch x := v.Init.(type) {
		case *ast.IfExpr:
			got = x.ElseChained
		case *ast.MatchExpr:
			got = x.ElseChained
		default:
			t.Fatalf("%s: initialiser should be *ast.IfExpr or *ast.MatchExpr; got %T", tc.name, v.Init)
		}
		if got != tc.chained {
			t.Errorf("%s: ElseChained = %v, want %v", tc.name, got, tc.chained)
		}
	}
}

func TestParseContextCancellationShortCircuits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
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

func TestParseContextHonoursBackgroundLikeOldParse(t *testing.T) {
	prog, err := ParseContext(context.Background(), "function f(): i32 { return 42; }")
	if err != nil {
		t.Fatalf("ParseContext: %v", err)
	}
	if len(prog.Funcs) != 1 || prog.Funcs[0].Name != "f" {
		t.Errorf("got %+v, want single func named f", prog.Funcs)
	}
}

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
	if _, err := Parse(`import "std/test" as;`); err == nil {
		t.Error("`import ... as;` with no alias name should be a parse error")
	}
}

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
	for _, src := range []string{`pub use "x".{};`, `pub use "x";`, `pub use "x".y;`} {
		if _, err := Parse(src); !hasParseCode(err, "P001") {
			t.Errorf("%s: err = %v, want P001", src, err)
		}
	}
}

func TestArenaIsNotReserved(t *testing.T) {
	prog, err := Parse("function f(): i32 { let arena: i32 = 5; return arena; }")
	if err != nil {
		t.Fatalf("`arena` should parse as an ordinary identifier: %v", err)
	}
	v, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Var)
	if !ok || v.Name != "arena" {
		t.Fatalf("expected `let arena`, got %T", prog.Funcs[0].Body.Stmts[0])
	}
}

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

func TestPathSepRejected(t *testing.T) {
	for _, src := range []string{
		"function main(): i32 { return Point::origin().x; }",
		"function main(): i32 { return helpers::add5(10); }",
		"function main(): i32 { return helpers::BONUS; }",
		"function main(): i32 { let b = m::Box[i32] { val: 1 }; return 0; }",
	} {
		_, err := Parse(src)
		var pe *Error
		if errs, ok := err.(diag.Errors); ok && len(errs) > 0 {
			pe, _ = errs[0].(*Error)
		}
		if pe == nil || pe.ErrCode != "P001" || pe.Pos.Col != strings.Index(src, "::")+1 || !strings.Contains(pe.Msg, "write `.`") {
			t.Errorf("%s: got %v, want a P001 at the `::` naming `.`", src, err)
		}
	}
}

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
	if _, ok := fn.BoundArgs["U"]; ok {
		t.Errorf("U (non-generic Eq bound) should not appear in BoundArgs")
	}
}

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

func TestInherentImplRejected(t *testing.T) {
	for _, tc := range []struct{ src, typ string }{
		{"struct Pt { x: i32 }\nimpl Pt {\n\tfunction sum(self: Self): i32 { return self.x; }\n}\nfunction main(): i32 { return 0; }", "Pt"},
		{"struct Box[T] { v: T }\nimpl[T] Box[T] {\n\tfunction of(v: T): Box[T] { return Box { v: v }; }\n}\nfunction main(): i32 { return 0; }", "Box[T]"},
	} {
		_, err := Parse(tc.src)
		errs, _ := err.(diag.Errors)
		if len(errs) != 1 {
			t.Errorf("%q: got %v, want exactly one error", tc.src, err)
			continue
		}
		pe, _ := errs[0].(*Error)
		want := "declare a method as `function (self: " + tc.typ + ") name(…)`"
		if pe == nil || pe.ErrCode != "P001" || pe.Pos.Line != 2 || !strings.Contains(pe.Msg, want) {
			t.Errorf("%q: got %v, want a P001 on line 2 containing %q", tc.src, err, want)
		}
	}
}

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
	dt, ok := byName["f"].Params[0].Type.(ast.DynTraitType)
	if !ok || len(dt.Traits) != 2 || dt.Traits[0] != "A" || dt.Traits[1] != "B" {
		t.Errorf("f param = %#v, want sorted dyn A + B", byName["f"].Params[0].Type)
	}
	if dt.String() != "dyn A + B" {
		t.Errorf("String() = %q, want %q", dt.String(), "dyn A + B")
	}
	dt3, ok := byName["g"].Params[0].Type.(ast.DynTraitType)
	if !ok || len(dt3.Traits) != 3 || dt3.Traits[0] != "A" || dt3.Traits[1] != "B" || dt3.Traits[2] != "C" {
		t.Errorf("g param = %#v, want sorted dyn A + B + C", byName["g"].Params[0].Type)
	}
	at, ok := byName["h"].Params[0].Type.(ast.ArrayType)
	if !ok {
		t.Fatalf("h param = %#v, want array", byName["h"].Params[0].Type)
	}
	if edt, ok := at.Elem.(ast.DynTraitType); !ok || len(edt.Traits) != 2 {
		t.Errorf("h element = %#v, want dyn A + B array", at.Elem)
	}
	if !ast.Equal(ast.NewDynTraitType("A", "B"), ast.NewDynTraitType("B", "A")) {
		t.Errorf("dyn A + B should equal dyn B + A")
	}
	if d := ast.NewDynTraitType("A", "A"); len(d.Traits) != 1 {
		t.Errorf("dyn A + A should dedup to [A], got %#v", d.Traits)
	}
	if _, err := Parse(`trait A { function a(self: Self): i32; }
function bad(d: dyn A +): i32 { return 0; }`); err == nil {
		t.Errorf("trailing `+` in dyn type should be a parse error")
	}
}

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
	at, ok := take.Params[0].Type.(ast.ArrayType)
	if !ok {
		t.Fatalf("take param = %#v, want array", take.Params[0].Type)
	}
	if h, ok := at.Elem.(ast.HandleType); !ok || h.Resource != "Pollable" || h.Borrowed {
		t.Errorf("take element = %#v, want own Pollable", at.Elem)
	}
}

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
	et, ok := impl.Type.(ast.EnumType)
	if !ok || et.Name != "Box" || len(et.Args) != 1 {
		t.Errorf("impl.Type = %s, want Box[T]", impl.Type)
	}
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

func TestParametricImplMethodOwnTypeParamsRejected(t *testing.T) {
	if _, err := Parse(`trait T { function f(self: Self): void; }
impl[A] T for Box[A] { function [B] (self: Self) f(): void {} }`); err == nil {
		t.Error("impl-method with its own type params inside a parametric impl should be a parse error")
	}
}

func TestTraitImplParseErrors(t *testing.T) {
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

func TestVarDeclRequiresInitializer(t *testing.T) {
	rejected := []string{
		`function main(): i32 { let x: i32; return x; }`,
		`function main(): i32 { let x; return 0; }`,
	}
	for _, src := range rejected {
		if _, err := Parse(src); err == nil {
			t.Errorf("let declaration without initializer should be a parse error:\n%s", src)
		}
	}
	for _, src := range []string{
		`function main(): i32 { let x: i32 = 0; return x; }`,
		`function main(): i32 { let x = 0; return x; }`,
	} {
		if _, err := Parse(src); err != nil {
			t.Errorf("initialized let declaration should parse: %v\n%s", err, src)
		}
	}
}

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

func TestTypeParamNoBounds(t *testing.T) {
	prog, err := Parse(`function id[T](x: T): T { return x; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(prog.Funcs[0].Bounds) != 0 {
		t.Errorf("expected no bounds, got %v", prog.Funcs[0].Bounds)
	}
}

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
	prog2, err := Parse(`@derive(Eq) pub struct P { x: i32 }`)
	if err != nil {
		t.Fatalf("@derive pub struct: %v", err)
	}
	if !prog2.Structs[0].Public || len(prog2.Structs[0].Derives) != 1 {
		t.Errorf("expected public struct with one derive, got %+v", prog2.Structs[0])
	}
	if _, err := Parse(`@derive(Eq) function f(): i32 { return 0; }`); err == nil {
		t.Error("@derive on a function should be a parse error")
	}
	if _, err := Parse(`@frobnicate(Eq) struct S { x: i32 }`); err == nil {
		t.Error("@frobnicate should be rejected")
	}
}

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

	prog2, err := Parse(`@import("a:b/c", "d") pub function f(): i32;`)
	if err != nil {
		t.Fatalf("@import pub function: %v", err)
	}
	if !prog2.Funcs[0].Public || prog2.Funcs[0].ImportIface != "a:b/c" {
		t.Errorf("expected public @import function, got %+v", prog2.Funcs[0])
	}
}

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
	prog2, err := Parse(`@export("a:b/c", "d") pub function f(): i32 { return 1; }`)
	if err != nil {
		t.Fatalf("@export pub function: %v", err)
	}
	if !prog2.Funcs[0].Public || prog2.Funcs[0].ExportIface != "a:b/c" {
		t.Errorf("expected public @export function, got %+v", prog2.Funcs[0])
	}
}

func TestExportAttributeErrors(t *testing.T) {
	if _, err := Parse(`@export("a:b/c", "d") function f(): i32;`); err == nil {
		t.Error("@export on a body-less function should be rejected")
	}
	if _, err := Parse(`@export("a:b/c", "d") struct S { x: i32 }`); err == nil {
		t.Error("@export on a struct should be rejected")
	}
}

func TestImportAttributeErrors(t *testing.T) {
	if _, err := Parse(`@import("a:b/c", "d") function f(): i32 { return 0; }`); err == nil {
		t.Error("@import function with a body should be a parse error")
	}
	if _, err := Parse(`@import("a:b/c", "d") struct S { x: i32 }`); err == nil {
		t.Error("@import on a struct should be a parse error")
	}
	if _, err := Parse(`function f(): i32;`); err == nil {
		t.Error("body-less function without @import should be a parse error")
	}
	if _, err := Parse(`@import("a:b/c") function f(): i32;`); err == nil {
		t.Error("@import with one argument should be a parse error")
	}
	if _, err := Parse(`@import(foo, bar) function f(): i32;`); err == nil {
		t.Error("@import with non-string arguments should be a parse error")
	}
}

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

func TestOpaqueStructParses(t *testing.T) {
	prog, err := Parse(`pub opaque struct Email { addr: string }`)
	if err != nil {
		t.Fatalf("opaque struct should parse: %v", err)
	}
	if !prog.Structs[0].Opaque || !prog.Structs[0].Public {
		t.Errorf("expected public opaque struct, got %+v", prog.Structs[0])
	}
	p2, err := Parse(`function f(): i32 { let opaque: i32 = 5; return opaque; }`)
	if err != nil {
		t.Fatalf("`opaque` should be a valid identifier: %v", err)
	}
	if v, ok := p2.Funcs[0].Body.Stmts[0].(*ast.Var); !ok || v.Name != "opaque" {
		t.Fatalf("expected `let opaque`, got %T", p2.Funcs[0].Body.Stmts[0])
	}
}

func TestArrowLambdaTupleReturnType(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
  let tup = (n: i32): (string, i32) => { return ("ab", n); };
  let arr = (): (string, i32)[] => { return [("a", 1)]; };
  let group = (): (i32) => { return 7; };
  let fnParen = (): ((i32) => i32) => { return (x: i32): i32 => x; };
  let fnBare = (): (i32) => i32 => { return (x: i32): i32 => x; };
  let fnTupleResult = (): ((i32) => (i32, i32)) => { return (x: i32): (i32, i32) => (x, x); };
  return 0;
}`)
	if err != nil {
		t.Fatal(err)
	}
	stmts := prog.Funcs[0].Body.Stmts
	want := []struct {
		name string
		kind string
	}{
		{"tup", "(string, i32)"},
		{"arr", "(string, i32)[]"},
		{"group", "i32"},
		{"fnParen", "(i32) => i32"},
		{"fnBare", "(i32) => i32"},
		{"fnTupleResult", "(i32) => (i32, i32)"},
	}
	for i, w := range want {
		v, ok := stmts[i].(*ast.Var)
		if !ok {
			t.Fatalf("stmt %d should be a Var, got %T", i, stmts[i])
		}
		if v.Name != w.name {
			t.Fatalf("stmt %d name = %q, want %q", i, v.Name, w.name)
		}
		lam, ok := v.Init.(*ast.Lambda)
		if !ok {
			t.Fatalf("%s init should be a Lambda, got %T", w.name, v.Init)
		}
		if got := lam.ReturnType.String(); got != w.kind {
			t.Errorf("%s return type = %q, want %q", w.name, got, w.kind)
		}
	}
}

func TestArrowLambdaParse(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
  let g: (i32, i32) => i32 = (a: i32, b: i32): i32 => a + b;
  let h: () => i32 = (): i32 => 42;
  let grouped: i32 = (1 + 2) * 3;
  let pair: (i32, i32) = (4, 5);
  return g(grouped, pair.0) + h();
}`)
	if err != nil {
		t.Fatal(err)
	}
	stmts := prog.Funcs[0].Body.Stmts
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
	if hl, ok := stmts[1].(*ast.Var).Init.(*ast.Lambda); !ok || len(hl.Params) != 0 {
		t.Errorf("h should be a zero-param Lambda, got %T", stmts[1].(*ast.Var).Init)
	}
	if _, ok := stmts[2].(*ast.Var).Init.(*ast.Lambda); ok {
		t.Errorf("(1 + 2) * 3 should not parse as a lambda")
	}
	if _, ok := stmts[3].(*ast.Var).Init.(*ast.TupleLit); !ok {
		t.Errorf("(4, 5) should parse as a TupleLit, got %T", stmts[3].(*ast.Var).Init)
	}
}

func TestArrayBuildDesugars(t *testing.T) {
	prog, err := Parse(`function f(): i32[] {
  return Array.build((b: ArrayBuilder[i32]): void => {
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
	v, ok := stmts[0].(*ast.Var)
	if !ok || v.Name != "b" {
		t.Fatalf("first stmt should be `let b`, got %T", stmts[0])
	}
	if at, ok := v.Type.(ast.ArrayType); !ok {
		t.Fatalf("b should be typed T[], got %v", v.Type)
	} else if _, ok := at.Elem.(ast.NumberType); !ok {
		t.Errorf("b's element type should be i32, got %v", at.Elem)
	}
	es, ok := stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("second stmt should be ExprStmt, got %T", stmts[1])
	}
	if _, ok := es.Expr.(*ast.Assign); !ok {
		t.Fatalf("b.append should desugar to an Assign, got %T", es.Expr)
	}
	w, ok := stmts[2].(*ast.While)
	if !ok {
		t.Fatalf("third stmt should be While, got %T", stmts[2])
	}
	inner := w.Body.(*ast.Block).Stmts[0].(*ast.ExprStmt)
	if _, ok := inner.Expr.(*ast.Assign); !ok {
		t.Errorf("nested b.append should desugar to an Assign, got %T", inner.Expr)
	}
	rb, ok := stmts[len(stmts)-1].(*ast.Return)
	if !ok {
		t.Fatalf("last stmt should be `return b`, got %T", stmts[len(stmts)-1])
	}
	if id, ok := rb.Value.(*ast.Ident); !ok || id.Name != "b" {
		t.Errorf("should return the builder local `b`, got %v", rb.Value)
	}
}

func TestArrayBuildMalformed(t *testing.T) {
	if _, err := Parse(`function f(): i32[] { return Array.build(42); }`); err == nil {
		t.Error("Array.build with a non-lambda argument should be a parse error")
	}
}

func TestMapBuildDesugars(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
  let m = Map.build((b: MapBuilder[i32, i32]): void => {
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
	bv, ok := lam.Body.Stmts[0].(*ast.Var)
	if !ok || bv.Name != "b" {
		t.Fatalf("first stmt should be `let b`, got %T", lam.Body.Stmts[0])
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
	es, ok := lam.Body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("second stmt should be ExprStmt, got %T", lam.Body.Stmts[1])
	}
	if _, ok := es.Expr.(*ast.Assign); !ok {
		t.Errorf("b.insert should desugar to an Assign, got %T", es.Expr)
	}
}

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

	prog2, err := Parse(`function f(a: i32): i32 { return a; } function main(): i32 { return f(1); }`)
	if err != nil {
		t.Fatalf("parse2: %v", err)
	}
	call2 := prog2.Funcs[1].Body.Stmts[0].(*ast.Return).Value.(*ast.Call)
	if call2.ArgNames != nil {
		t.Errorf("all-positional call should have nil ArgNames, got %#v", call2.ArgNames)
	}
}

func TestParseDowncastVsCast(t *testing.T) {
	prog, err := Parse(`function f(s: dyn Shape): i32 { let c: Option[Circle] = s as? Circle; return 0; }`)
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

func TestParseValuePositionBlockExpr(t *testing.T) {
	prog, err := Parse(`function f(): i32 { let n: i32 = { let k = 3; k * 2 }; return n; }`)
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

	prog2, err := Parse(`struct P { x: i32 } function g(): i32 { let p: P = P { x: 1 }; return p.x; }`)
	if err != nil {
		t.Fatalf("parse struct lit: %v", err)
	}
	if _, ok := prog2.Funcs[0].Body.Stmts[0].(*ast.Var).Init.(*ast.StructLit); !ok {
		t.Fatalf("`P { x: 1 }` must stay a *ast.StructLit, got %T",
			prog2.Funcs[0].Body.Stmts[0].(*ast.Var).Init)
	}

	if _, err := Parse(`function h(xs: i32[]): i32 { let s = 0; for x in xs { s = s + x; } return s; }`); err != nil {
		t.Fatalf("for-in header must still parse (its `{` is the body): %v", err)
	}
	prog3, err := Parse(`function k(): i32 { let n: i32 = { 7 }; return n; }`)
	if err != nil {
		t.Fatalf("parse single-expr block: %v", err)
	}
	if _, ok := prog3.Funcs[0].Body.Stmts[0].(*ast.Var).Init.(*ast.BlockExpr); ok {
		t.Errorf("`{ 7 }` must collapse to the bare expr `7`, not a BlockExpr")
	}
}

func TestParseControlFlowInBlockExpr(t *testing.T) {
	prog, err := Parse(`function f(e: i32): i32 { let x: i32 = { if (e < 0) { return 9; } e + 1 }; return x; }`)
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

	prog2, err := Parse(`function g(c: boolean): i32 { let x: i32 = if (c) { 1 } else { 2 }; return x; }`)
	if err != nil {
		t.Fatalf("parse if-expr branch: %v", err)
	}
	if _, ok := prog2.Funcs[0].Body.Stmts[0].(*ast.Var).Init.(*ast.IfExpr); !ok {
		t.Errorf("if-with-else init = %T, want *ast.IfExpr",
			prog2.Funcs[0].Body.Stmts[0].(*ast.Var).Init)
	}
}

func TestParseStrViewType(t *testing.T) {
	prog, err := Parse(`function f(v: str, vs: str[]): str {
    let w: str = v;
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

	if _, err := Parse(`function g(): i32 { let str: i32 = 2; return str; }`); err != nil {
		t.Errorf("`str` as an identifier: %v", err)
	}
	prog2, err := Parse(`function h(x: str.Foo): void {}`)
	if err != nil {
		t.Fatalf("str.Foo: %v", err)
	}
	st, ok := prog2.Funcs[0].Params[0].Type.(ast.StructType)
	if !ok || st.Name != "str.Foo" {
		t.Errorf("str.Foo: got %#v, want StructType{Name: \"str.Foo\"}", prog2.Funcs[0].Params[0].Type)
	}
}

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

	if _, err := Parse(`function f(n: i32): i32 { match (n) { k @ _ => { return k; } } }`); err == nil {
		t.Errorf("`k @ _` parsed, want a diagnostic")
	} else if !strings.Contains(err.Error(), "`_` pattern") {
		t.Errorf("`k @ _` diagnostic = %v, want it to name the `_` pattern", err)
	}
}

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
	if len(paren.Bindings) != 1 || paren.Bindings[0] != "" {
		t.Errorf("paren form Bindings = %v, want one empty name (the sub-pattern binds instead)", paren.Bindings)
	}
	if len(paren.Payloads) != 1 || paren.Payloads[0] == nil {
		t.Fatalf("paren form should carry a payload sub-pattern, got %v", paren.Payloads)
	}
	if got := paren.Payloads[0]; got.VariantName != "Err2" || len(got.VariantBindings) != 0 {
		t.Errorf("sub-pattern = %q/%v, want the payload-less variant Err2", got.VariantName, got.VariantBindings)
	}
	if len(paren.Body.Stmts) != 1 {
		t.Fatalf("paren form body should be the arm body unchanged, got %d stmts", len(paren.Body.Stmts))
	}
	if _, nested := paren.Body.Stmts[0].(*ast.Match); nested {
		t.Error("paren form must not desugar to an inner match any more")
	}
}

func TestUnterminatedReceiverClauseDoesNotPanic(t *testing.T) {
	for _, src := range []string{
		"function(A:",
		"function (A:0",
		"function(own a: T",
		"function(a: Box[",
	} {
		if _, err := Parse(src); err == nil {
			t.Errorf("expected a parse error for %q", src)
		}
	}
}

func TestIntegerLiteralPastU64ParsesFlagged(t *testing.T) {
	cases := []struct {
		text      string
		wantWidth int
	}{
		{"18446744073709551616", 0},
		{"99999999999999999999999", 0},
		{"0xFFFFFFFFFFFFFFFFFFFF", 0},
		{"18446744073709551616u64", 64},
	}
	for _, c := range cases {
		prog, err := Parse(`function main(): i32 { return ` + c.text + `; }`)
		if err != nil {
			t.Errorf("%s: parse error %v, want the literal kept", c.text, err)
			continue
		}
		ret, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
		if !ok {
			t.Fatalf("%s: statement is %T, want *ast.Return", c.text, prog.Funcs[0].Body.Stmts[0])
		}
		lit, ok := ret.Value.(*ast.NumberLit)
		if !ok {
			t.Errorf("%s: returned %T, want *ast.NumberLit", c.text, ret.Value)
			continue
		}
		raw := strings.TrimSuffix(c.text, "u64")
		if !lit.ExceedsU64 || lit.ExceedsI64 || lit.Value != 0 || lit.Raw != raw || lit.Width != c.wantWidth {
			t.Errorf("%s: got ExceedsU64=%v ExceedsI64=%v Value=%d Raw=%q Width=%d, want ExceedsU64 with Raw %q and Width %d",
				c.text, lit.ExceedsU64, lit.ExceedsI64, lit.Value, lit.Raw, lit.Width, raw, c.wantWidth)
		}
		if lit.P.Col != 31 {
			t.Errorf("%s: literal at column %d, want 31", c.text, lit.P.Col)
		}
	}
	prog, err := Parse(`function main(): i32 { return 18446744073709551615; }`)
	if err != nil {
		t.Fatal(err)
	}
	lit := prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value.(*ast.NumberLit)
	if lit.ExceedsU64 || !lit.ExceedsI64 || uint64(lit.Value) != 18446744073709551615 {
		t.Errorf("u64 max: got ExceedsU64=%v ExceedsI64=%v Value=%d", lit.ExceedsU64, lit.ExceedsI64, lit.Value)
	}
}

func TestQualifiedStructLitTakesPostfix(t *testing.T) {
	for _, src := range []string{
		`function main(): i32 { return lib.Thing { v: 1 }.v; }`,
		`function main(): i32 { return Box[i32] { v: 1 }.v; }`,
	} {
		prog, err := Parse(src)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		ret, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
		if !ok {
			t.Fatalf("%s: first statement is %T", src, prog.Funcs[0].Body.Stmts[0])
		}
		fa, ok := ret.Value.(*ast.FieldAccess)
		if !ok || fa.Field != "v" {
			t.Errorf("%s: return value is %T, want a .v field access", src, ret.Value)
			continue
		}
		if _, ok := fa.Target.(*ast.StructLit); !ok {
			t.Errorf("%s: field access target is %T, want the struct literal", src, fa.Target)
		}
	}
}

func TestParametricImplKeepsBoundArgs(t *testing.T) {
	prog, err := Parse(`trait Iter[T] { function next(self: Self): Option[(T, Self)]; }
struct W[I, T] { it: I, k: T }
impl[I: Iter[T], T] Iter[T] for W[I, T] {
  function next(self: W[I, T]): Option[(T, W[I, T])] { return None; }
}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(prog.Impls) != 1 {
		t.Fatalf("impls = %d, want 1", len(prog.Impls))
	}
	check := func(what string, args map[string][][]ast.Type) {
		t.Helper()
		ba := args["I"]
		if len(ba) != 1 || len(ba[0]) != 1 || ba[0][0].String() != "T" {
			t.Errorf("%s BoundArgs[I] = %v, want [[T]]", what, ba)
		}
	}
	check("impl", prog.Impls[0].BoundArgs)
	for _, fn := range prog.Funcs {
		if fn.Name == "next" {
			check("method", fn.BoundArgs)
			return
		}
	}
	t.Fatal("method next not desugared")
}

func TestTraitDefaultOwnTypeParams(t *testing.T) {
	prog, err := Parse(`trait Iter[T] {
  function next(self: Self): Option[(T, Self)];
  function fold[A: Copy](self: Self, init: A, f: (A, T) => A): A { return init; }
}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	m := prog.Traits[0].Methods[1]
	if len(m.TypeParams) != 1 || m.TypeParams[0] != "A" || len(m.Bounds["A"]) != 1 || m.Bounds["A"][0] != "Copy" {
		t.Errorf("fold type params = %v bounds = %v, want [A] with A: Copy", m.TypeParams, m.Bounds)
	}
	if _, err := Parse(`trait Iter[T] { function map[U](self: Self, f: (T) => U): U; }`); err == nil || !strings.Contains(err.Error(), "must have a default body") {
		t.Errorf("required method with its own type parameters: got %v", err)
	}
}

func TestLaneTypeKeywords(t *testing.T) {
	prog, err := Parse(`function f(a: f64x2): f32x4 { let b: f32x4 = __f32x4_splat(1.0); return b; }`)
	if err != nil {
		t.Fatalf("lane types should parse: %v", err)
	}
	fn := prog.Funcs[0]
	for _, c := range []struct {
		got   ast.Type
		width int
		text  string
	}{
		{fn.Params[0].Type, 64, "f64x2"},
		{fn.ReturnType, 32, "f32x4"},
		{fn.Body.Stmts[0].(*ast.Var).Type, 32, "f32x4"},
	} {
		lt, ok := c.got.(ast.LanesType)
		if !ok || lt.Width != c.width || lt.String() != c.text {
			t.Errorf("want %s (width %d), got %#v", c.text, c.width, c.got)
		}
	}
	if _, err := Parse(`function f(): i32 { let f64x2: i32 = 1; return f64x2; }`); err == nil {
		t.Error("a lane type keyword should not parse as a binding name")
	}
}

func TestGenericSupertraitArguments(t *testing.T) {
	prog, err := Parse(`trait Child[U]: api.Parent[Option[U], (string, u8)] + Marker {}`)
	if err != nil {
		t.Fatal(err)
	}
	td := prog.Traits[0]
	if len(td.Supertraits) != 2 || td.Supertraits[0] != "api.Parent" || td.Supertraits[1] != "Marker" {
		t.Fatalf("parents: %v", td.Supertraits)
	}
	if len(td.SupertraitArgs) != 2 || len(td.SupertraitArgs[0]) != 2 || len(td.SupertraitArgs[1]) != 0 {
		t.Fatalf("arguments: %v", td.SupertraitArgs)
	}
	for i, want := range []string{"Option[U]", "(string, u8)"} {
		if got := td.SupertraitArgs[0][i].String(); got != want {
			t.Errorf("argument %d: %s, want %s", i, got, want)
		}
	}
}
