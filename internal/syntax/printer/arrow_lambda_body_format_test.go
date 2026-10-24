package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestFormatArrowLambdaBodyShapes(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{
			"expression body", `function main(): i32 { let f = (x: i32) => x * 2; return f(1); }`,
			"  let f = (x: i32) => x * 2;\n",
		},
		{
			"single return", `function main(): i32 { let f = (x: i32) => { return x * 2; }; return f(1); }`,
			"  let f = (x: i32) => x * 2;\n",
		},
		{
			"statement-only body", `function main(): i32 { let n: i32 = 0; let f = (x: i32) => { n = n + x; }; f(1); return n; }`,
			"  let f = (x: i32) => { n = n + x; };\n",
		},
		{
			"empty body", `function main(): i32 { let f = (x: i32) => {}; f(1); return 0; }`,
			"  let f = (x: i32) => {};\n",
		},
		{
			"multi-statement body", `function main(): i32 { let f = (x: i32) => { let y: i32 = x + 1; return y * 2; }; return f(1); }`,
			"  let f = (x: i32) => {\n    let y: i32 = x + 1;\n    return y * 2;\n  };\n",
		},
		{
			"annotated return type", `function main(): i32 { let n: i32 = 0; let f = (x: i32): void => { n = n + x; }; f(1); return n; }`,
			"  let f = (x: i32): void => { n = n + x; };\n",
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

func TestFormatArrowLambdaReturnTypeParens(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{
			"tuple return", `function main(): i32 { let g = (): (string, i32) => { return ("ab", 7); }; let t = g(); return t.1; }`,
			"  let g = (): (string, i32) => (\"ab\", 7);\n",
		},
		{
			"grouped scalar return", `function main(): i32 { let g = (): (i32) => { return 7; }; return g(); }`,
			"  let g = (): i32 => 7;\n",
		},
		{
			"function return keeps its parens", `function main(): i32 { let h = (p: i32): ((i32) => i32) => (q: i32) => p + q; return h(1)(2); }`,
			"  let h = (p: i32): ((i32) => i32) => (q: i32) => p + q;\n",
		},
		{
			"function return gains parens", `function main(): i32 { let h = (p: i32): (i32) => i32 => (q: i32) => p + q; return h(1)(2); }`,
			"  let h = (p: i32): ((i32) => i32) => (q: i32) => p + q;\n",
		},
		{
			"function returning a tuple", `function main(): i32 { let m = (p: i32): ((i32) => (i32, i32)) => (q: i32) => (p, q); let r = m(1)(2); return r.0 + r.1; }`,
			"  let m = (p: i32): ((i32) => (i32, i32)) => (q: i32) => (p, q);\n",
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
