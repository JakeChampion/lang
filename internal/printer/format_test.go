package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

// formatSrc parses src, formats it, and returns the formatted text.
// Most tests both check the exact string and verify idempotence /
// round-trip via reparse.
func formatSrc(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return Format(prog)
}

// A trivial function lays out across multiple lines with two-space
// indentation; the body opens with `{` on the same line as the
// signature and closes with `}` aligned to the function's column.
func TestFormatSimpleFunction(t *testing.T) {
	got := formatSrc(t, `function f(): number { return 42; }`)
	want := "function f(): number {\n  return 42;\n}\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// Nested blocks indent further. `if` / `else` chain stays on the
// same line as the closing brace of the previous arm.
func TestFormatIfElseIndents(t *testing.T) {
	got := formatSrc(t, `function f(n: number): number { if (n == 0) { return 1; } else { return n; } }`)
	want := strings.Join([]string{
		"function f(n: number): number {",
		"  if (n == 0) {",
		"    return 1;",
		"  } else {",
		"    return n;",
		"  }",
		"}",
		"",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// Empty blocks stay one-liners (`{}`) so an `if (c) {}` doesn't
// burst into three lines unnecessarily.
func TestFormatEmptyBlockStaysCompact(t *testing.T) {
	got := formatSrc(t, `function f(): void {}`)
	want := "function f(): void {}\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// Operator precedence drives parenthesisation: `1 + 2 * 3` formats
// without parens (mul binds tighter), but `(1 + 2) * 3` keeps them
// because the parser's tree groups + on the left.
func TestFormatMinimalParens(t *testing.T) {
	cases := []struct{ in, want string }{
		{`1 + 2 * 3`, `1 + 2 * 3`},
		{`(1 + 2) * 3`, `(1 + 2) * 3`},
		{`1 * 2 + 3`, `1 * 2 + 3`},
		{`1 - 2 - 3`, `1 - 2 - 3`},                   // left-assoc, no parens
		{`1 - (2 - 3)`, `1 - (2 - 3)`},               // right-of-left-assoc keeps parens
		{`a && b || c`, `a && b || c`},               // && binds tighter than ||
		{`a && (b || c)`, `a && (b || c)`},           // explicit grouping preserved
	}
	for _, tc := range cases {
		got := formatSrc(t, "function f(a: boolean, b: boolean, c: boolean): number { return "+tc.in+"; }")
		// The relevant fragment is on the second line, between
		// "  return " and ";".
		if !strings.Contains(got, "return "+tc.want+";") {
			t.Errorf("input %q → expected `return %s;` in:\n%s", tc.in, tc.want, got)
		}
	}
}

// Negative number / float literals format as unary `-` over a
// positive literal, matching how the parser models them.
func TestFormatNegativeLiterals(t *testing.T) {
	got := formatSrc(t, `function f(): number { return -7; }`)
	if !strings.Contains(got, "return -7;") {
		t.Errorf("expected `return -7;` in:\n%s", got)
	}
	got = formatSrc(t, `function f(): float { return -1.5; }`)
	if !strings.Contains(got, "return -1.5;") {
		t.Errorf("expected `return -1.5;` in:\n%s", got)
	}
}

// Floats with no fractional part get a `.0` so re-lex still
// classifies them as Float, not Number.
func TestFormatFloatLiteralKeepsDecimal(t *testing.T) {
	got := formatSrc(t, `function f(): float { return 5.0; }`)
	if !strings.Contains(got, "5.0") {
		t.Errorf("expected `5.0` to survive in:\n%s", got)
	}
}

// Method declarations preserve the receiver clause.
func TestFormatMethod(t *testing.T) {
	got := formatSrc(t, `struct Point { x: number, y: number }
function (p: Point) sum(): number { return p.x + p.y; }`)
	if !strings.Contains(got, "function (p: Point) sum(): number {") {
		t.Errorf("expected method receiver clause to survive:\n%s", got)
	}
}

// Switch statements indent each case and the optional default; the
// case bodies use the same multi-line block formatting.
func TestFormatSwitch(t *testing.T) {
	got := formatSrc(t, `function f(n: number): number {
		switch (n) {
			case 1, 2: return 10;
			case 3: return 30;
			default: return 0;
		}
		return -1;
	}`)
	if !strings.Contains(got, "  switch (n) {") {
		t.Errorf("switch header should be indented:\n%s", got)
	}
	if !strings.Contains(got, "    case 1, 2: ") {
		t.Errorf("case should be indented one further:\n%s", got)
	}
	if !strings.Contains(got, "    default: ") {
		t.Errorf("default should be indented one further:\n%s", got)
	}
}

// `for (init; cond; step)` keeps its three-clause shape with
// semicolons separating the slots; init's trailing `;` is part of
// the Var/ExprStmt, step has no trailing `;`.
func TestFormatForLoop(t *testing.T) {
	got := formatSrc(t, `function f(): number {
		var sum = 0;
		for (var i = 0; i < 3; i = i + 1) { sum = sum + i; }
		return sum;
	}`)
	if !strings.Contains(got, "for (var i = 0; i < 3; i = i + 1) {") {
		t.Errorf("for-header shape lost:\n%s", got)
	}
}

// Format → parse → Format is byte-stable: a second pass produces
// identical output. Idempotence is the contract every formatter
// honours so editors can run it on every save without churn.
func TestFormatIsIdempotent(t *testing.T) {
	src := `struct Point { x: number, y: number }
function (p: Point) magnitude(): number { return p.x * p.x + p.y * p.y; }
function factorial(n: number, acc: number): number {
	if (n == 0) { return acc; }
	return factorial(n - 1, acc * n);
}
function main(): number {
	var origin = Point { x: 3, y: 4 };
	return origin.magnitude() + factorial(5, 1);
}`
	first := formatSrc(t, src)
	second := formatSrc(t, first)
	if first != second {
		t.Errorf("format not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// parse → Format → parse must round-trip the AST shape (modulo the
// known comments-and-blank-lines limitation). The check is "does
// the formatted output reparse without errors".
func TestFormatRoundTripsThroughParser(t *testing.T) {
	srcs := []string{
		`function f(): number { return 1 + 2 * 3; }`,
		`function f(a: number, b: number): number { return a < b ? a : b; }`,
		`function f(s: string): boolean { return s == "x"; }`,
		`function f(): number { var a: number[] = [1, 2, 3]; return a[1]; }`,
		`function f(n: number): number {
			if (n == 0) { return 1; }
			while (n > 0) { n = n - 1; }
			return n;
		}`,
		`struct P { x: number, y: number }
function f(p: P): number { return p.x + p.y; }`,
	}
	for _, src := range srcs {
		formatted := formatSrc(t, src)
		if _, err := parser.Parse(formatted); err != nil {
			t.Errorf("formatted output failed to reparse:\n%s\nerror: %v", formatted, err)
		}
	}
}

// Trailing newline at end of file — every editor expects it; many
// VCSs flag its absence as a diff hazard.
func TestFormatEndsWithNewline(t *testing.T) {
	got := formatSrc(t, `function f(): number { return 0; }`)
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("output must end with newline; got %q", got)
	}
}

// Leading line comments above a statement re-emit on their own
// lines at the statement's indent level. The lexer captures them;
// the parser threads them through prog.Comments; the formatter
// drains them just before each statement.
func TestFormatPreservesLeadingComment(t *testing.T) {
	src := `function main(): number {
  // why we return 42
  return 42;
}`
	got := formatSrc(t, src)
	if !strings.Contains(got, "  // why we return 42\n  return 42;") {
		t.Errorf("expected leading comment preserved at statement indent:\n%s", got)
	}
}

// Trailing comments on the same source line as a single-line
// statement re-emit inline, separated by two spaces — the
// `putchar(70);  // F` shape.
func TestFormatPreservesTrailingComment(t *testing.T) {
	src := `function main(): void {
  putchar(70);  // F
  putchar(66);  // B
}`
	got := formatSrc(t, src)
	if !strings.Contains(got, "putchar(70);  // F") {
		t.Errorf("expected trailing comment preserved inline:\n%s", got)
	}
	if !strings.Contains(got, "putchar(66);  // B") {
		t.Errorf("expected second trailing comment preserved inline:\n%s", got)
	}
}

// File-level comments before the first declaration emit at the
// top of the output at depth 0.
func TestFormatPreservesFileLeadingComment(t *testing.T) {
	src := `// program description
function main(): number { return 0; }`
	got := formatSrc(t, src)
	if !strings.HasPrefix(got, "// program description\n") {
		t.Errorf("expected leading file comment at top:\n%s", got)
	}
}

// Comments after the last declaration emit at end-of-file before
// the trailing newline.
func TestFormatPreservesTrailingFileComment(t *testing.T) {
	src := `function main(): number { return 0; }
// outro comment`
	got := formatSrc(t, src)
	if !strings.Contains(got, "// outro comment\n") {
		t.Errorf("expected trailing file comment preserved:\n%s", got)
	}
}

// Idempotence still holds with comments — formatting twice
// produces the same output.
func TestFormatIdempotentWithComments(t *testing.T) {
	src := `// header
function f(): number {
  // before return
  return 7;  // trailing
}
// outro`
	first := formatSrc(t, src)
	second := formatSrc(t, first)
	if first != second {
		t.Errorf("comment-bearing format not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// Multiple top-level decls are separated by a single blank line so
// they read clearly without bunching.
func TestFormatBlankLineBetweenTopLevelDecls(t *testing.T) {
	got := formatSrc(t, `function a(): number { return 1; }
function b(): number { return 2; }`)
	// Expect two functions separated by exactly one blank line —
	// `}\n\nfunction b…` (closing brace, newline, blank line,
	// next function).
	if !strings.Contains(got, "}\n\nfunction b") {
		t.Errorf("expected blank line between top-level functions:\n%s", got)
	}
}

// `pub` round-trips: the formatter emits it back in front of
// `function` / `struct` so private vs exported decls stay
// distinguishable in formatted source.
func TestFormatPubKeywordRoundTrips(t *testing.T) {
	got := formatSrc(t, `pub struct Point { x: number, y: number }
pub function exposed(): number { return 1; }
function hidden(): number { return 2; }`)
	if !strings.Contains(got, "pub struct Point") {
		t.Errorf("expected `pub struct Point` in output:\n%s", got)
	}
	if !strings.Contains(got, "pub function exposed") {
		t.Errorf("expected `pub function exposed` in output:\n%s", got)
	}
	if strings.Contains(got, "pub function hidden") {
		t.Errorf("private `function hidden` should not gain a `pub`:\n%s", got)
	}
}
