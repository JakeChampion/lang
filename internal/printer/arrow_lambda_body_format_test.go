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
