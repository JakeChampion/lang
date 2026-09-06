package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// An arrow lambda reprints as an arrow lambda, whatever its body shape (#2673).
//
// A braced body's statements are the lambda's own, so it no longer matches the
// single `return expr` an expression body desugars to. Falling back to
// `function(…)` for it would state a return type nobody wrote — and `void` is a
// lie for every body that yields a value, so the formatted program stops
// compiling, which is #7338.
func TestFormatArrowLambdaBodyShapes(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{
			"expression body", `function main(): i32 { var f = (x: i32) => x * 2; return f(1); }`,
			"  var f = (x: i32) => x * 2;\n",
		},
		{
			// A one-statement `return` body reads as the expression it wraps.
			"single return", `function main(): i32 { var f = (x: i32) => { return x * 2; }; return f(1); }`,
			"  var f = (x: i32) => x * 2;\n",
		},
		{
			"statement-only body", `function main(): i32 { var n: i32 = 0; var f = (x: i32) => { n = n + x; }; f(1); return n; }`,
			"  var f = (x: i32) => { n = n + x; };\n",
		},
		{
			"empty body", `function main(): i32 { var f = (x: i32) => {}; f(1); return 0; }`,
			"  var f = (x: i32) => {};\n",
		},
		{
			// The body indents against the statement it sits in, not against
			// column zero.
			"multi-statement body", `function main(): i32 { var f = (x: i32) => { var y: i32 = x + 1; return y * 2; }; return f(1); }`,
			"  var f = (x: i32) => {\n    var y: i32 = x + 1;\n    return y * 2;\n  };\n",
		},
		{
			"annotated return type", `function main(): i32 { var n: i32 = 0; var f = (x: i32): void => { n = n + x; }; f(1); return n; }`,
			"  var f = (x: i32): void => { n = n + x; };\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSrc(t, tc.in)
			if !strings.Contains(got, tc.want) {
				t.Errorf("formatted output is missing\n%q\ngot:\n%s", tc.want, got)
			}
			// The formatted program must still parse, still type-check, and
			// format to itself.
			prog, err := parser.Parse(got)
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			if _, err := checker.Check(prog); err != nil {
				t.Errorf("formatted program no longer checks: %v", err)
			}
			if second := formatSrc(t, got); second != got {
				t.Errorf("format not idempotent:\nfirst:\n%s\nsecond:\n%s", got, second)
			}
		})
	}
}

// An arrow lambda's return annotation is followed by the lambda's own `=>`, so
// the type parser reserves the top-level function-type arrow there (#8706,
// #8717). A function-typed return therefore has to be reprinted with its
// grouping parens: without them `(p: i32): (i32) => (i32, i32) => …` re-parses
// with the wrong split, which is #7338's class.
func TestFormatArrowLambdaReturnTypeParens(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{
			"tuple return", `function main(): i32 { var g = (): (string, i32) => { return ("ab", 7); }; var t = g(); return t.1; }`,
			"  var g = (): (string, i32) => (\"ab\", 7);\n",
		},
		{
			// A single-element annotation is grouping, and prints as the type
			// it groups.
			"grouped scalar return", `function main(): i32 { var g = (): (i32) => { return 7; }; return g(); }`,
			"  var g = (): i32 => 7;\n",
		},
		{
			"function return keeps its parens", `function main(): i32 { var h = (p: i32): ((i32) => i32) => (q: i32) => p + q; return h(1)(2); }`,
			"  var h = (p: i32): ((i32) => i32) => (q: i32) => p + q;\n",
		},
		{
			// Written bare, it parses — the greedy read finds the lambda's
			// arrow after the result — but it must not print back bare.
			"function return gains parens", `function main(): i32 { var h = (p: i32): (i32) => i32 => (q: i32) => p + q; return h(1)(2); }`,
			"  var h = (p: i32): ((i32) => i32) => (q: i32) => p + q;\n",
		},
		{
			"function returning a tuple", `function main(): i32 { var m = (p: i32): ((i32) => (i32, i32)) => (q: i32) => (p, q); var r = m(1)(2); return r.0 + r.1; }`,
			"  var m = (p: i32): ((i32) => (i32, i32)) => (q: i32) => (p, q);\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSrc(t, tc.in)
			if !strings.Contains(got, tc.want) {
				t.Errorf("formatted output is missing\n%q\ngot:\n%s", tc.want, got)
			}
			prog, err := parser.Parse(got)
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			if _, err := checker.Check(prog); err != nil {
				t.Errorf("formatted program no longer checks: %v", err)
			}
			if second := formatSrc(t, got); second != got {
				t.Errorf("format not idempotent:\nfirst:\n%s\nsecond:\n%s", got, second)
			}
		})
	}
}
