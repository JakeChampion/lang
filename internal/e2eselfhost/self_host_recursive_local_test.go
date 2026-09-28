package e2eselfhost

import (
	"testing"
)

// recursiveLocalCases cover self-recursive local functions, which the
// self-host desugars to `var f = lambda` and then lifts to a top-level
// function in hoist_local_funcs_module (recursion resolves once it's
// top-level). Covers direct self-recursion, tree recursion, and a recursive
// local that calls a top-level function. Exit codes cross-checked vs the Go
// backend (which supports recursive locals natively). Every exit stays below
// 126, which a wasm run cannot report (#2908).
var recursiveLocalCases = []struct {
	name string
	src  string
	exit int
}{
	{"factorial", "function main(): i32 { function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } return fact(5); }", 120},
	{"fib", "function main(): i32 { function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } return fib(10); }", 55},
	{"calls-toplevel", "function dbl(x: i32): i32 { return x * 2; } function main(): i32 { function sumto(n: i32): i32 { if (n <= 0) { return 0; } return dbl(1) + sumto(n - 1); } return sumto(5); }", 10},
	// Statements *around* the hoisted recursive local must survive the
	// rebuild (regression: the lift dropped non-lifted `var`s that shared
	// the body). A plain var before it, and a capturing closure alongside.
	{"var-before", "function main(): i32 { var x: i32 = 5; function r(n: i32): i32 { if (n <= 0) { return 0; } return r(n - 1); } return x + r(3); }", 5},
	{"with-sibling-closure", "function main(): i32 { var base: i32 = 100; var add = (x: i32): i32 => { return x + base; }; function cd(n: i32): i32 { if (n <= 0) { return 0; } return 1 + cd(n - 1); } return add(cd(5) + 17); }", 122},
	// Capturing recursive locals: lambda-lifted with the captured enclosing
	// names threaded through as trailing params + at every call site. Each
	// capture parameter has its binding's written or inferred type (#10457).
	{"capture-one", "function main(): i32 { var base: i32 = 10; function f(n: i32): i32 { if (n <= 0) { return base; } return 1 + f(n - 1); } return f(3); }", 13},
	{"capture-two", "function main(): i32 { var acc: i32 = 0; var step: i32 = 2; function go(n: i32): i32 { if (n <= 0) { return acc; } return step + go(n - 1); } return go(4); }", 8},
	{"capture-2calls", "function main(): i32 { var base: i32 = 50; function f(n: i32): i32 { if (n <= 0) { return base; } return 1 + f(n - 1); } return f(2) + f(3); }", 105},
	// An arrow lambda naming its `var` reads the enclosing binding of that
	// name, so it is not a recursive local and must not be lifted (#10383).
	{"arrow-reads-outer", "function main(): i32 { var f = (x: i32): i32 => { return x + 1; }; if (true) { var f = (x: i32): i32 => { return f(x) * 2; }; return f(3); } return 0; }", 8},
	{"capture-inferred", "function main(): i32 { var base = 7; function f(n: i32): i32 { if (n <= 0) { return base; } return 1 + f(n - 1); } return f(3); }", 10},
	// A counted capture: the lifted function's parameter is a string.
	{"capture-string", "function main(): i32 { var s = \"abc\"; function f(n: i32): i32 { if (n <= 0) { return s.len(); } return 1 + f(n - 1); } return f(3); }", 6},
}

func TestSelfHostRecursiveLocal(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range recursiveLocalCases {
		t.Run(tc.name, func(t *testing.T) {
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.exit {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.exit, stderr)
				}
			}
		})
	}
}
