package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// A braced arrow-lambda body is the lambda's own body block (#2673).
//
// `=>` takes an expression, so a `{ … }` body first parses as a block
// EXPRESSION — but wrapping that in a `return` is what made a body with no
// value of its own (`(x) => { f(x); }`) a value-less block in value position
// (E061) rather than the void lambda `(x) => { f(x); }` has always been.
// Splicing the block in instead makes every body shape mean what the same
// statements mean in a named function's body, with a trailing value written
// without a `;` becoming the returned value.
func TestArrowLambdaBracedBodyIsSpliced(t *testing.T) {
	lambdaOf := func(t *testing.T, src string) *ast.Lambda {
		t.Helper()
		prog, err := Parse(src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		var got *ast.Lambda
		ast.WalkProgram(prog, func(n ast.Node) bool {
			if l, ok := n.(*ast.Lambda); ok && got == nil {
				got = l
			}
			return true
		})
		if got == nil {
			t.Fatal("no lambda in the parsed program")
		}
		return got
	}

	t.Run("statement-only body keeps its statements and adds no return", func(t *testing.T) {
		l := lambdaOf(t, `function main(): i32 { var f = (x: i32) => { g(x); h(x); }; return 0; }`)
		if len(l.Body.Stmts) != 2 {
			t.Fatalf("want the 2 body statements, got %d: %#v", len(l.Body.Stmts), l.Body.Stmts)
		}
		for i, st := range l.Body.Stmts {
			if _, ok := st.(*ast.Return); ok {
				t.Errorf("statement %d is a Return; a valueless body returns nothing", i)
			}
		}
	})

	t.Run("empty body is an empty block", func(t *testing.T) {
		l := lambdaOf(t, `function main(): i32 { var f = (x: i32) => {}; return 0; }`)
		if len(l.Body.Stmts) != 0 {
			t.Fatalf("want no body statements, got %d: %#v", len(l.Body.Stmts), l.Body.Stmts)
		}
	})

	t.Run("trailing value becomes the returned value", func(t *testing.T) {
		l := lambdaOf(t, `function main(): i32 { var f = (x: i32) => { var y: i32 = x; y * 2 }; return 0; }`)
		if len(l.Body.Stmts) != 2 {
			t.Fatalf("want the var plus a return, got %d: %#v", len(l.Body.Stmts), l.Body.Stmts)
		}
		ret, ok := l.Body.Stmts[1].(*ast.Return)
		if !ok {
			t.Fatalf("want the tail value as a Return, got %T", l.Body.Stmts[1])
		}
		if _, ok := ret.Value.(*ast.BlockExpr); ok {
			t.Error("the tail value is returned directly, not as a block expression")
		}
	})

	t.Run("explicit return is the body's own statement", func(t *testing.T) {
		l := lambdaOf(t, `function main(): i32 { var f = (x: i32) => { return x * 2; }; return 0; }`)
		if len(l.Body.Stmts) != 1 {
			t.Fatalf("want the single return, got %d: %#v", len(l.Body.Stmts), l.Body.Stmts)
		}
		ret, ok := l.Body.Stmts[0].(*ast.Return)
		if !ok {
			t.Fatalf("want a Return, got %T", l.Body.Stmts[0])
		}
		if _, ok := ret.Value.(*ast.BlockExpr); ok {
			t.Error("the body block is spliced in, so nothing returns a block expression")
		}
	})

	// #8561: an `if` / `match` STATEMENT among the body's statements. Both stay
	// on the block scanner's EXPRESSION path, because either can also be the
	// block's trailing value, so an item followed by neither `;` nor `}` has to
	// be read again as the statement it is.
	t.Run("match statement followed by more statements", func(t *testing.T) {
		l := lambdaOf(t, `function main(): i32 { var f = (x: i32) => {
			match (x) { 0 => { return 100; }, _ => {} }
			return x * 2;
		}; return f(1); }`)
		if len(l.Body.Stmts) != 2 {
			t.Fatalf("want the match plus the return, got %d: %#v", len(l.Body.Stmts), l.Body.Stmts)
		}
		if _, ok := l.Body.Stmts[0].(*ast.Match); !ok {
			t.Errorf("want the match as a statement, got %T", l.Body.Stmts[0])
		}
	})

	t.Run("if-with-else statement followed by more statements", func(t *testing.T) {
		l := lambdaOf(t, `function main(): i32 { var f = (x: i32) => {
			if (x > 0) { g(x); } else { h(x); }
			return x * 2;
		}; return f(1); }`)
		if len(l.Body.Stmts) != 2 {
			t.Fatalf("want the if plus the return, got %d: %#v", len(l.Body.Stmts), l.Body.Stmts)
		}
		if _, ok := l.Body.Stmts[0].(*ast.If); !ok {
			t.Errorf("want the if as a statement, got %T", l.Body.Stmts[0])
		}
	})

	t.Run("a trailing match is still the block's value", func(t *testing.T) {
		l := lambdaOf(t, `function main(): i32 { var f = (x: i32) => {
			var y: i32 = x + 1;
			match (y) { 0 => 100, _ => y * 2 }
		}; return f(1); }`)
		if len(l.Body.Stmts) != 2 {
			t.Fatalf("want the var plus a return, got %d: %#v", len(l.Body.Stmts), l.Body.Stmts)
		}
		ret, ok := l.Body.Stmts[1].(*ast.Return)
		if !ok {
			t.Fatalf("want the tail match returned, got %T", l.Body.Stmts[1])
		}
		if _, ok := ret.Value.(*ast.MatchExpr); !ok {
			t.Errorf("want a match expression as the returned value, got %T", ret.Value)
		}
	})

	// #8593: `use` and `let … else` rewrite the REST of the block they appear
	// in, so they only work where the parser has that remainder to give them —
	// which a value-block scanner never does. A braced body is a function body.
	t.Run("use binds the rest of the body", func(t *testing.T) {
		l := lambdaOf(t, `function apply(x: i32, cb: (i32) => i32): i32 { return cb(x); }
function main(): i32 {
    var f = (): i32 => {
        use n <- apply(41);
        return n + 1;
    };
    return f();
}`)
		// The desugar makes the rest of the body a callback and returns the
		// call with it appended: a local FuncDecl for the callback, then
		// `return apply(41, <callback>)`.
		if len(l.Body.Stmts) != 2 {
			t.Fatalf("want the callback decl plus the return, got %d: %#v", len(l.Body.Stmts), l.Body.Stmts)
		}
		if _, ok := l.Body.Stmts[0].(*ast.FuncDecl); !ok {
			t.Fatalf("want the synthesised callback declaration, got %T", l.Body.Stmts[0])
		}
		ret, ok := l.Body.Stmts[1].(*ast.Return)
		if !ok {
			t.Fatalf("want a Return, got %T", l.Body.Stmts[1])
		}
		call, ok := ret.Value.(*ast.Call)
		if !ok {
			t.Fatalf("want the call `use` desugars to, got %T", ret.Value)
		}
		if len(call.Args) != 2 {
			t.Errorf("want the callback appended as the last argument, got %d args", len(call.Args))
		}
	})

	t.Run("let-else binds the rest of the body", func(t *testing.T) {
		l := lambdaOf(t, `enum O { Has(i32), Nil }
function main(): i32 {
    var f = (): i32 => {
        var o: O = Nil;
        let Has(v) = o else { return 0; };
        return v;
    };
    return f();
}`)
		// The desugar turns the `let` and everything after it into a match.
		if len(l.Body.Stmts) != 2 {
			t.Fatalf("want the var plus the desugared match, got %d: %#v", len(l.Body.Stmts), l.Body.Stmts)
		}
		m, ok := l.Body.Stmts[1].(*ast.Match)
		if !ok {
			t.Fatalf("want the let-else match, got %T", l.Body.Stmts[1])
		}
		if m.Origin != ast.OriginLetElse {
			t.Errorf("want a let-else match, got origin %v", m.Origin)
		}
	})

	t.Run("an expression body still returns that expression", func(t *testing.T) {
		l := lambdaOf(t, `function main(): i32 { var f = (x: i32) => x * 2; return 0; }`)
		if len(l.Body.Stmts) != 1 {
			t.Fatalf("want the single return, got %d: %#v", len(l.Body.Stmts), l.Body.Stmts)
		}
		ret, ok := l.Body.Stmts[0].(*ast.Return)
		if !ok {
			t.Fatalf("want a Return, got %T", l.Body.Stmts[0])
		}
		if _, ok := ret.Value.(*ast.Binary); !ok {
			t.Errorf("want the expression returned directly, got %T", ret.Value)
		}
	})
}
