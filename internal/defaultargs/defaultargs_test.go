package defaultargs

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

func fill(t *testing.T, src string) (*ast.Program, []Error) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v\nsrc:\n%s", err, src)
	}
	return prog, Fill(prog)
}

// callArgs returns the argument list of the first call to name.
func callArgs(prog *ast.Program, name string) []ast.Expr {
	var found []ast.Expr
	ast.RewriteProgramExprs(prog, func(e ast.Expr) ast.Expr {
		if c, ok := e.(*ast.Call); ok && found == nil {
			if id, ok := c.Callee.(*ast.Ident); ok && id.Name == name {
				found = c.Args
			}
		}
		return e
	})
	return found
}

// A default is copied into the CALL SITE, so a name inside it resolved in
// the caller's scope instead of the callee's (#8445): `b: i32 = a * 2`
// read the CALLER's `a`, and `f(1)` returned 201 rather than 3. Nothing
// downstream could see it — the pass rewrites the AST before type-checking,
// so the interpreter produced the same wrong answer as every backend.
func TestDefaultMustBeConstantExpression(t *testing.T) {
	cases := []struct {
		name, src, wantName string
	}{
		{
			name: "reads-another-parameter",
			src: `function f(a: i32, b: i32 = a * 2): i32 { return a + b; }
function main(): i32 { var a: i32 = 100; return f(1); }`,
			wantName: "a",
		},
		{
			name: "calls-a-module-function",
			src: `function size(): i32 { return 8; }
function f(n: i32 = size()): i32 { return n; }
function main(): i32 { return f(); }`,
			wantName: "size",
		},
		{
			name: "reads-a-bare-name",
			src: `function f(n: i32 = limit): i32 { return n; }
function main(): i32 { return f(); }`,
			wantName: "limit",
		},
		{
			name: "nested-inside-arithmetic",
			src: `function f(a: i32, b: i32 = 1 + (a * 2)): i32 { return a + b; }
function main(): i32 { return f(1); }`,
			wantName: "a",
		},
		// The check hunted for free IDENTIFIERS and descended into Ident /
		// Unary / Binary / Call only, so a name reached the call site intact
		// whenever it was wrapped in any other node. All four of these were
		// accepted, and each read the caller's value at run time: the
		// field-access one returned 42 from the caller's `config` (#8445,
		// found in review of #8503). The check is a whitelist now, so the
		// shape is named rather than the name inside it.
		{
			name: "field-access",
			src: `struct Config { timeout: i32 }
function f(a: i32, b: i32 = config.timeout): i32 { return a + b; }
function main(): i32 { var config: Config = Config { timeout: 41 }; return f(1); }`,
			wantName: "a field access",
		},
		{
			name: "index",
			src: `function f(a: i32, b: i32 = xs[0]): i32 { return a + b; }
function main(): i32 { var xs: i32[] = [41, 9]; return f(1); }`,
			wantName: "an index",
		},
		{
			name: "cast",
			src: `function f(a: i32, b: i32 = n as i32): i32 { return a + b; }
function main(): i32 { var n: i64 = 41; return f(1); }`,
			wantName: "a cast",
		},
		{
			name: "lambda",
			src: `function f(a: i32, g: (i32) => i32 = (x: i32) => x + n): i32 { return g(a); }
function main(): i32 { var n: i32 = 41; return f(1); }`,
			wantName: "a lambda",
		},
		{
			name: "struct-literal",
			src: `struct P { v: i32 }
function f(a: i32, p: P = P { v: n }): i32 { return a + p.v; }
function main(): i32 { var n: i32 = 41; return f(1); }`,
			wantName: "a struct literal",
		},
		{
			name: "array-literal",
			src: `function f(a: i32, xs: i32[] = [n]): i32 { return a + xs[0]; }
function main(): i32 { var n: i32 = 41; return f(1); }`,
			wantName: "an array literal",
		},
		// Nesting one inside arithmetic must not smuggle it past either.
		{
			name: "field-access-under-arithmetic",
			src: `struct Config { timeout: i32 }
function f(a: i32, b: i32 = 1 + config.timeout): i32 { return a + b; }
function main(): i32 { var config: Config = Config { timeout: 41 }; return f(1); }`,
			wantName: "a field access",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := fill(t, tc.src)
			if len(errs) == 0 {
				t.Fatalf("accepted a default reading %q; it would be resolved in the caller's scope\nsrc:\n%s", tc.wantName, tc.src)
			}
			if errs[0].Code != "E076" {
				t.Errorf("got code %q, want E076", errs[0].Code)
			}
			if !strings.Contains(errs[0].Msg, tc.wantName) {
				t.Errorf("message does not name %q: %s", tc.wantName, errs[0].Msg)
			}
		})
	}
}

// The restriction must not cost the shapes defaults are actually written in.
func TestConstantDefaultsStillFill(t *testing.T) {
	cases := []struct {
		name, src string
		wantArgs  int
	}{
		{
			name: "number-literal",
			src: `function listen(port: i32, backlog: i32 = 128): i32 { return port + backlog; }
function main(): i32 { return listen(80); }`,
			wantArgs: 2,
		},
		{
			name: "string-literal",
			src: `function greet(name: string, greeting: string = "hello"): string { return greeting + name; }
function main(): i32 { var s: string = greet("x"); return 0; }`,
			wantArgs: 2,
		},
		{
			name: "bool-literal",
			src: `function go(a: i32, verbose: boolean = true): i32 { return a; }
function main(): i32 { return go(1); }`,
			wantArgs: 2,
		},
		{
			name: "arithmetic-over-literals",
			src: `function scale(x: i32, factor: i32 = 2 * 3): i32 { return x * factor; }
function main(): i32 { return scale(1); }`,
			wantArgs: 2,
		},
		{
			name: "negative-literal",
			src: `function off(x: i32, delta: i32 = -1): i32 { return x + delta; }
function main(): i32 { return off(1); }`,
			wantArgs: 2,
		},
		{
			name: "two-defaults-one-supplied",
			src: `function f(a: i32, b: i32 = 2, c: i32 = 3): i32 { return a + b + c; }
function main(): i32 { return f(1); }`,
			wantArgs: 3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, errs := fill(t, tc.src)
			if len(errs) != 0 {
				t.Fatalf("rejected a constant default: %s (%s)\nsrc:\n%s", errs[0].Msg, errs[0].Code, tc.src)
			}
			callee := strings.SplitN(strings.TrimPrefix(tc.src, "function "), "(", 2)[0]
			if got := callArgs(prog, callee); len(got) != tc.wantArgs {
				t.Errorf("call to %s has %d args after filling, want %d", callee, len(got), tc.wantArgs)
			}
		})
	}
}

// Named-argument errors used to be reported as E060, which the catalogue
// documents as "invalid `as?` downcast target" — so `fern -explain` answered
// with an unrelated page. They have their own code now.
func TestNamedArgumentErrorsUseTheirOwnCode(t *testing.T) {
	cases := []struct{ name, src string }{
		{
			name: "unknown-parameter-name",
			src: `function listen(port: i32, backlog: i32 = 128): i32 { return port; }
function main(): i32 { return listen(port = 80, backlogg = 5); }`,
		},
		{
			name: "duplicate-argument",
			src: `function listen(port: i32, backlog: i32 = 128): i32 { return port; }
function main(): i32 { return listen(80, port = 81); }`,
		},
		{
			name: "positional-after-named",
			src: `function listen(port: i32, backlog: i32 = 128): i32 { return port; }
function main(): i32 { return listen(port = 80, 5); }`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := fill(t, tc.src)
			if len(errs) == 0 {
				t.Fatalf("accepted an invalid named-argument call\nsrc:\n%s", tc.src)
			}
			if errs[0].Code != "E077" {
				t.Errorf("got code %q, want E077 (E060 is the `as?` downcast code)", errs[0].Code)
			}
		})
	}
}

// Named arguments that are valid must still reorder into positional order.
func TestNamedArgumentsReorder(t *testing.T) {
	src := `function f(a: i32, b: i32, c: i32 = 3): i32 { return a + b + c; }
function main(): i32 { return f(b = 2, a = 1); }`
	prog, errs := fill(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected error: %s", errs[0].Msg)
	}
	args := callArgs(prog, "f")
	if len(args) != 3 {
		t.Fatalf("got %d args, want 3", len(args))
	}
	for i, want := range []int64{1, 2, 3} {
		lit, ok := args[i].(*ast.NumberLit)
		if !ok {
			t.Fatalf("arg %d is %T, want a number literal", i, args[i])
		}
		if lit.Value != want {
			t.Errorf("arg %d = %d, want %d (named arguments must reorder positionally)", i, lit.Value, want)
		}
	}
}
