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
	got := formatSrc(t, `function f(): i32 { return 42; }`)
	want := "function f(): i32 {\n  return 42;\n}\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// Nested blocks indent further. `if` / `else` chain stays on the
// same line as the closing brace of the previous arm.
func TestFormatIfElseIndents(t *testing.T) {
	got := formatSrc(t, `function f(n: i32): i32 { if (n == 0) { return 1; } else { return n; } }`)
	want := strings.Join([]string{
		"function f(n: i32): i32 {",
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
		{`1 - 2 - 3`, `1 - 2 - 3`},         // left-assoc, no parens
		{`1 - (2 - 3)`, `1 - (2 - 3)`},     // right-of-left-assoc keeps parens
		{`a && b || c`, `a && b || c`},     // && binds tighter than ||
		{`a && (b || c)`, `a && (b || c)`}, // explicit grouping preserved
	}
	for _, tc := range cases {
		got := formatSrc(t, "function f(a: boolean, b: boolean, c: boolean): i32 { return "+tc.in+"; }")
		// The relevant fragment is on the second line, between
		// "  return " and ";".
		if !strings.Contains(got, "return "+tc.want+";") {
			t.Errorf("input %q → expected `return %s;` in:\n%s", tc.in, tc.want, got)
		}
	}
}

// Bitwise (& | ^) bind LOOSER than the comparison family (== != < <=
// > >=), matching Fern's parser hierarchy (parseLogicalAnd → parseBitOr
// → parseBitXor → parseBitAnd → parseEquality → parseRelational in
// parser.go). A printer that ranked bitwise tighter would drop parens
// the parser actually needs on round-trip — the bug that turned
// `(n & (n - 1)) == 0` (the "is power of 2" idiom) into
// `n & ((n - 1) == 0)` (ANDing a number with a boolean) when round-
// tripping internal/stdlib/std/i32.fern. This unit-level guard pins
// the contract so the same regression can't sneak back in without
// the corpus sweep noticing.
func TestFormatBitwiseLooserThanCompare(t *testing.T) {
	cases := []struct{ in, want string }{
		// `==` binds tighter → keep the parens around `n & m` so the
		// AST still groups as `(n & m) == 0` after re-parse.
		{`(a & b) == 0`, `(a & b) == 0`},
		{`(a | b) == 0`, `(a | b) == 0`},
		{`(a ^ b) == 0`, `(a ^ b) == 0`},
		// `!=` / `<` / `>` family same precedence as `==`.
		{`(a & b) != 0`, `(a & b) != 0`},
		{`(a & b) < 16`, `(a & b) < 16`},
		// The bare `a == b & c` shape (parens already absent in
		// source) keeps that grouping verbatim: parser reads it as
		// `(a == b) & c` so a round-trip preserves it without
		// needing to add parens. Pinned so a future precedence
		// reshuffle doesn't silently insert them.
		{`a == b & c`, `a == b & c`},
		// `&` vs `|` — `&` binds tighter (parseBitOr → parseBitXor
		// → parseBitAnd), so `a & b | c` drops parens around
		// `a & b`. `a | b & c` keeps the grouping verbatim.
		{`a & b | c`, `a & b | c`},
		{`a | b & c`, `a | b & c`},
		{`(a | b) & c`, `(a | b) & c`},
	}
	for _, tc := range cases {
		got := formatSrc(t, "function f(a: i32, b: i32, c: i32): i32 { return "+tc.in+"; }")
		if !strings.Contains(got, "return "+tc.want+";") {
			t.Errorf("input %q → expected `return %s;` in:\n%s", tc.in, tc.want, got)
		}
	}
}

// Negative i32 / float literals format as unary `-` over a
// positive literal, matching how the parser models them.
func TestFormatNegativeLiterals(t *testing.T) {
	got := formatSrc(t, `function f(): i32 { return -7; }`)
	if !strings.Contains(got, "return -7;") {
		t.Errorf("expected `return -7;` in:\n%s", got)
	}
	got = formatSrc(t, `function f(): f32 { return -1.5; }`)
	if !strings.Contains(got, "return -1.5;") {
		t.Errorf("expected `return -1.5;` in:\n%s", got)
	}
}

// Floats with no fractional part get a `.0` so re-lex still
// classifies them as Float, not Number.
func TestFormatFloatLiteralKeepsDecimal(t *testing.T) {
	got := formatSrc(t, `function f(): f32 { return 5.0; }`)
	if !strings.Contains(got, "5.0") {
		t.Errorf("expected `5.0` to survive in:\n%s", got)
	}
}

// Method declarations preserve the receiver clause.
func TestFormatMethod(t *testing.T) {
	got := formatSrc(t, `struct Point { x: i32, y: i32 }
function (p: Point) sum(): i32 { return p.x + p.y; }`)
	if !strings.Contains(got, "function (p: Point) sum(): i32 {") {
		t.Errorf("expected method receiver clause to survive:\n%s", got)
	}
}

// Switch statements indent each case and the optional default; the
// case bodies use the same multi-line block formatting.
func TestFormatSwitch(t *testing.T) {
	got := formatSrc(t, `function f(n: i32): i32 {
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
	got := formatSrc(t, `function f(): i32 {
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
	src := `struct Point { x: i32, y: i32 }
function (p: Point) magnitude(): i32 { return p.x * p.x + p.y * p.y; }
function factorial(n: i32, acc: i32): i32 {
	if (n == 0) { return acc; }
	return factorial(n - 1, acc * n);
}
function main(): i32 {
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
		`function f(): i32 { return 1 + 2 * 3; }`,
		`function f(a: i32, b: i32): i32 { return if (a < b) { a } else { b }; }`,
		// Typed numeric literal suffixes — formatter must round-trip.
		`function f(): i64 { return 42i64; }`,
		`function f(): u8 { return 7u8; }`,
		`function f(): f64 { return 1.5f64; }`,
		`function f(s: string): boolean { return s == "x"; }`,
		`function f(): i32 { var a: i32[] = [1, 2, 3]; return a[1]; }`,
		`function f(n: i32): i32 {
			if (n == 0) { return 1; }
			while (n > 0) { n = n - 1; }
			return n;
		}`,
		`struct P { x: i32, y: i32 }
function f(p: P): i32 { return p.x + p.y; }`,
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
	got := formatSrc(t, `function f(): i32 { return 0; }`)
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("output must end with newline; got %q", got)
	}
}

// Leading line comments above a statement re-emit on their own
// lines at the statement's indent level. The lexer captures them;
// the parser threads them through prog.Comments; the formatter
// drains them just before each statement.
func TestFormatPreservesLeadingComment(t *testing.T) {
	src := `function main(): i32 {
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
function main(): i32 { return 0; }`
	got := formatSrc(t, src)
	if !strings.HasPrefix(got, "// program description\n") {
		t.Errorf("expected leading file comment at top:\n%s", got)
	}
}

// Comments after the last declaration emit at end-of-file before
// the trailing newline.
func TestFormatPreservesTrailingFileComment(t *testing.T) {
	src := `function main(): i32 { return 0; }
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
function f(): i32 {
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
	got := formatSrc(t, `function a(): i32 { return 1; }
function b(): i32 { return 2; }`)
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
	got := formatSrc(t, `pub struct Point { x: i32, y: i32 }
pub function exposed(): i32 { return 1; }
function hidden(): i32 { return 2; }`)
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

// Top-level `const` formats on its own line with the optional type
// annotation preserved. `pub const` round-trips like other `pub`
// decls; the resulting source reparses identically.
func TestFormatConstDeclRoundTrips(t *testing.T) {
	got := formatSrc(t, `const N: i32 = 42;
pub const PI: f32 = 3.14;
const M = 7;
function main(): i32 { return N; }`)
	for _, want := range []string{
		"const N: i32 = 42;",
		"pub const PI: f32 = 3.14;",
		"const M = 7;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output:\n%s", want, got)
		}
	}
	// The format should be idempotent — second pass produces the
	// same text.
	again := formatSrc(t, got)
	if got != again {
		t.Errorf("format not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

// Generic enum decls and generic instantiations at type
// positions round-trip through the formatter. The single-line
// enum form keeps `[T]` next to the name; type positions
// preserve `Option[i32]` rather than collapsing the
// brackets.
func TestFormatGenericEnumRoundTrip(t *testing.T) {
	got := formatSrc(t, `enum Option[T] { Some(T), None }
function find(): Option[i32] { return None; }`)
	if !strings.Contains(got, "enum Option[T] { Some(T), None }") {
		t.Errorf("expected generic enum decl with [T]; got:\n%s", got)
	}
	if !strings.Contains(got, "Option[i32]") {
		t.Errorf("expected `Option[i32]` in return-type position; got:\n%s", got)
	}
	again := formatSrc(t, got)
	if got != again {
		t.Errorf("format not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

// f-strings round-trip through parse → format → parse: the
// surface `f"..."` syntax survives a formatter pass instead of
// collapsing to its desugared `+`-chain. Covers empty f-string,
// literal-only, multi-interpolation, brace escapes, escape
// sequences in literal segments, and an interpolant that
// contains arithmetic.
func TestFormatFStringRoundTrip(t *testing.T) {
	cases := []struct {
		in   string
		want string // substring expected in output
	}{
		{in: `function f(): string { return f""; }`, want: `f""`},
		{in: `function f(): string { return f"plain"; }`, want: `f"plain"`},
		{in: `function f(x: i32): string { return f"v={x}"; }`, want: `f"v={x}"`},
		{in: `function f(a: i32, b: i32): string { return f"sum {a + b}"; }`, want: `f"sum {a + b}"`},
		{in: `function f(): string { return f"{{lit}}"; }`, want: `f"{{lit}}"`},
		{in: `function f(): string { return f"hi\nthere"; }`, want: `f"hi\nthere"`},
	}
	for _, c := range cases {
		got := formatSrc(t, c.in)
		if !strings.Contains(got, c.want) {
			t.Errorf("output missing %q for input %q:\n%s", c.want, c.in, got)
		}
		again := formatSrc(t, got)
		if got != again {
			t.Errorf("not idempotent for input %q:\nfirst:\n%s\nsecond:\n%s", c.in, got, again)
		}
	}
}

// `defer` statements round-trip through the formatter — for both
// bare-call expressions and method-call expressions on a receiver.
// The method-call shape was previously eaten silently because the
// statement printer's switch had no `case *ast.Defer` arm; the
// result was an empty line where `defer r.close();` had stood.
func TestFormatDeferRoundTrip(t *testing.T) {
	srcs := []string{
		`function f(): void { defer cleanup(); }`,
		`function f(r: Reader): void { defer r.close(); }`,
		`function f(r: Reader, w: Writer): void {
defer r.close();
defer w.close();
}`,
	}
	for _, src := range srcs {
		got := formatSrc(t, src)
		if !strings.Contains(got, "defer ") {
			t.Errorf("`defer` keyword stripped from output for input %q:\n%s", src, got)
		}
		again := formatSrc(t, got)
		if got != again {
			t.Errorf("format not idempotent for input %q:\nfirst:\n%s\nsecond:\n%s", src, got, again)
		}
	}
}

// An anonymous function expression (lambda) used as a call argument
// must survive formatting. Before the fix formatExpr had no
// `*ast.Lambda` case, so it fell through to the empty default and
// dropped the lambda entirely — when the lambda was the last argument
// the output was `f(xs, )`, which then failed to re-parse. Regression
// guard: the lambda text survives, and parse → format → parse is
// stable. Surfaced by the examples-corpus formatter sweep.
func TestFormatLambdaArgumentRoundTrip(t *testing.T) {
	srcs := []string{
		// last-argument lambda — the dangling-comma case
		`function f(): Option[string] { return check([1, 2, 3], function(n: i32): boolean { return n > 0; }); }`,
		// lambda bound to a local
		`function f(): i32 { var g = function(x: i32): i32 { return x + 1; }; return g(41); }`,
		// multi-statement body (block form, not inlined)
		`function f(): i32 { var g = function(x: i32): i32 { var y = x * 2; return y + 1; }; return g(20); }`,
	}
	for _, src := range srcs {
		got := formatSrc(t, src)
		if !strings.Contains(got, "function(") {
			t.Errorf("lambda dropped from formatted output for input %q:\n%s", src, got)
		}
		if _, err := parser.Parse(got); err != nil {
			t.Errorf("formatted output failed to re-parse for input %q:\n%s\nerr: %v", src, got, err)
		}
		again := formatSrc(t, got)
		if got != again {
			t.Errorf("format not idempotent for input %q:\nfirst:\n%s\nsecond:\n%s", src, got, again)
		}
	}
}

// `enum` decls and `match` statements round-trip through
// parse → format → parse stably, including payload-carrying
// variants and `pub enum`.
func TestFormatEnumAndMatchRoundTrip(t *testing.T) {
	got := formatSrc(t, `pub enum Status { Ok, Err(string) }
function f(s: Status): i32 {
match (s) { Ok => { return 0; }, Err(msg) => { return msg.len(); } }
return 0;
}`)
	for _, want := range []string{
		"pub enum Status { Ok, Err(string) }",
		"match (s) {",
		"Ok => {",
		"Err(msg) => {",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output:\n%s", want, got)
		}
	}
	again := formatSrc(t, got)
	if got != again {
		t.Errorf("format not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

// Imports round-trip through the formatter — previously
// dropped silently because the Format loop only walked
// structs / enums / unions / consts / funcs. As the
// prelude-to-modules migration moves test programs and
// examples to `import "core/no_prelude";`-style explicit
// declarations, `fern -fmt -w` would have stripped every
// import line and the fmt-check CI gate would fail.
func TestFormatImportsRoundTrip(t *testing.T) {
	got := formatSrc(t, `import "core/no_prelude";
import "std/i32";
function main(): i32 { return 0; }`)
	for _, want := range []string{
		`import "core/no_prelude";`,
		`import "std/i32";`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output:\n%s", want, got)
		}
	}
	again := formatSrc(t, got)
	if got != again {
		t.Errorf("format not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

// Qualified-variant references (`Color.Red`) round-trip through
// the multi-line formatter in both expression and match-arm
// positions. Was IMPROVEMENTS.md #15 — without the printer change
// `fern -fmt -w` would silently drop the `Color.` prefix users
// wrote to disambiguate two enums that share a variant name.
func TestFormatQualifiedVariantsRoundTrip(t *testing.T) {
	got := formatSrc(t, `enum A { Foo(i32), Bar }
enum B { Foo(i32), Baz }
function main(): i32 {
	var a: A = A.Foo(11);
	match (a) {
		A.Foo(x) => { return x; },
		A.Bar => { return 0; }
	}
	return 0;
}`)
	for _, want := range []string{
		"var a: A = A.Foo(11);",
		"A.Foo(x) =>",
		"A.Bar =>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output:\n%s", want, got)
		}
	}
	again := formatSrc(t, got)
	if got != again {
		t.Errorf("format not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

// Union types (`type X = A | B | C;`) round-trip through the
// formatter — previously dropped silently because no
// `formatUnionDecl` path existed. Members preserved in source
// order so the checker desugar's variant-tag assignment stays
// stable across `fern -fmt -w` edits.
func TestFormatUnionDeclRoundTrip(t *testing.T) {
	got := formatSrc(t, `struct Add { l: i32, r: i32 }
struct Mul { l: i32, r: i32 }
pub type Expr = Add | Mul;
function main(): i32 { return 0; }`)
	for _, want := range []string{
		"struct Add { l: i32, r: i32 }",
		"struct Mul { l: i32, r: i32 }",
		"pub type Expr = Add | Mul;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output:\n%s", want, got)
		}
	}
	again := formatSrc(t, got)
	if got != again {
		t.Errorf("format not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

// Format round-trips an import alias: `import "p" as a;` prints the
// `as` clause, and re-parsing the output preserves the alias.
func TestFormatImportAlias(t *testing.T) {
	out := formatSrc(t, `import "std/test" as t;
import "std/string";
function main(): i32 { return 0; }`)
	if !strings.Contains(out, `import "std/test" as t;`) {
		t.Errorf("formatted output missing aliased import:\n%s", out)
	}
	if !strings.Contains(out, `import "std/string";`) {
		t.Errorf("formatted output missing plain import:\n%s", out)
	}
	// Re-parse the formatted text; the alias must survive.
	prog, err := parser.Parse(out)
	if err != nil {
		t.Fatalf("reparse formatted output: %v\n%s", err, out)
	}
	if prog.Imports[0].Alias != "t" {
		t.Errorf("alias lost on round-trip: %+v", prog.Imports[0])
	}
}

// Blank lines between statements inside a block are preserved as a
// single separator (runs collapse to one); a leading blank just inside
// the opening brace is dropped, and no blank is invented where the
// source had none.
func TestFormatPreservesBlankLines(t *testing.T) {
	src := "function f(): i32 {\n\n  var x = 1;\n  var y = 2;\n\n\n  return x + y;\n}\n"
	got := formatSrc(t, src)
	want := strings.Join([]string{
		"function f(): i32 {",
		"  var x = 1;", // leading blank after `{` dropped
		"  var y = 2;",
		"", // the author's separator (two source blanks collapsed to one)
		"  return x + y;",
		"}",
		"",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
	// Idempotent: formatting the output again is a no-op.
	if again := formatSrc(t, got); again != got {
		t.Errorf("not idempotent:\nfirst:\n%q\nsecond:\n%q", got, again)
	}
}

// A blank line above a leading comment counts as the separator for the
// statement the comment introduces.
func TestFormatBlankLineAboveComment(t *testing.T) {
	src := "function f(): i32 {\n  var x = 1;\n\n  // next group\n  return x;\n}\n"
	got := formatSrc(t, src)
	want := strings.Join([]string{
		"function f(): i32 {",
		"  var x = 1;",
		"",
		"  // next group",
		"  return x;",
		"}",
		"",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// Format must emit an `@import` extern as the attribute + a body-less `;`
// signature, not silently rewrite it into an empty-body function (which would
// drop the binding and change semantics).
func TestFormatExternImport(t *testing.T) {
	out := formatSrc(t, `@import("wasi:random/random@0.2.0", "get-random-u64") function r(): u64;`)
	if !strings.Contains(out, `@import("wasi:random/random@0.2.0", "get-random-u64")`) {
		t.Errorf("formatted output dropped the @import attribute:\n%s", out)
	}
	if !strings.Contains(out, "function r(): u64;") {
		t.Errorf("formatted output is not a body-less extern:\n%s", out)
	}
	if strings.Contains(out, "{}") {
		t.Errorf("extern must not gain an empty body:\n%s", out)
	}
}

// Format must preserve `@derive(...)` on structs and enums — dropping it
// silently removed the derived trait impls (a semantics change).
func TestFormatDeriveAttr(t *testing.T) {
	out := formatSrc(t, `@derive(Eq, Display) struct P { x: i32, y: i32 } function main(): i32 { return 0; }`)
	if !strings.Contains(out, "@derive(Eq, Display)") {
		t.Errorf("formatted output dropped @derive on a struct:\n%s", out)
	}
	eout := formatSrc(t, `@derive(Eq) enum Color { Red, Green } function main(): i32 { return 0; }`)
	if !strings.Contains(eout, "@derive(Eq)") {
		t.Errorf("formatted output dropped @derive on an enum:\n%s", eout)
	}
}
