package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
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

// The contextual function modifiers — `fip` / `fbip` (bare + graded) and
// `async` — carry checked semantics (E053/E068; the P3 async export), so
// the formatter must re-emit them rather than drop them silently.
func TestFormatKeepsFunctionModifiers(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`fip function f(x: i32): i32 { return x; }`, "fip function f(x: i32): i32 {\n  return x;\n}\n"},
		{`fbip function f(x: i32): i32 { return x; }`, "fbip function f(x: i32): i32 {\n  return x;\n}\n"},
		{`pub fip(2) function f(x: i32): i32 { return x; }`, "pub fip(2) function f(x: i32): i32 {\n  return x;\n}\n"},
		{`fbip(1) function f(x: i32): i32 { return x; }`, "fbip(1) function f(x: i32): i32 {\n  return x;\n}\n"},
		{`async function f(): i32 { return 0; }`, "async function f(): i32 {\n  return 0;\n}\n"},
	} {
		if got := formatSrc(t, tc.in); got != tc.want {
			t.Errorf("format(%q):\ngot  %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

// An unsigned literal whose magnitude exceeds i64::MAX is stored by
// the parser as a negative int64 bit pattern (via ParseUint). The
// formatter must render it back as the unsigned decimal, not via
// `-x.Value` (which overflowed for the 2^63 / math.MinInt64 pattern
// and produced a spurious `--`). These are the boundary values: 2^63,
// (2^32-1)^2, and u64::MAX.
func TestFormatUnsignedLargeLiteral(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`function main(): i32 { var a: u64 = 9223372036854775808 as u64; return 0; }`,
			"9223372036854775808 as u64"},
		{`function main(): i32 { var a: u64 = 18446744065119617025 as u64; return 0; }`,
			"18446744065119617025 as u64"},
		{`function main(): i32 { var a: u64 = 18446744073709551615 as u64; return 0; }`,
			"18446744073709551615 as u64"},
	} {
		got := formatSrc(t, tc.in)
		if !strings.Contains(got, tc.want) {
			t.Errorf("format(%q):\ngot  %q\nwant substring %q", tc.in, got, tc.want)
		}
		if strings.Contains(got, "--") || strings.Contains(got, "-9223372036854775808") {
			t.Errorf("format(%q) emitted a spurious negation: %q", tc.in, got)
		}
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

// The `::` path separator (`Type::method`, `mod::func`, `mod::CONST`)
// round-trips through Format — it is not normalised to `.`. See #2700.
func TestFormatPathSep(t *testing.T) {
	got := formatSrc(t, `import "./helpers";
function main(): i32 {
    var a: i32 = Point::origin().x;
    return a + helpers::add5(10) + helpers::BONUS;
}`)
	for _, want := range []string{"Point::origin()", "helpers::add5(10)", "helpers::BONUS"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q preserved in:\n%s", want, got)
		}
	}
	// The ordinary `.x` field access stays a dot.
	if !strings.Contains(got, "Point::origin().x") {
		t.Errorf("`.x` should stay a dot:\n%s", got)
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

// Every `for … in …` surface form reprints as itself rather than as its
// parse-time desugar (#6770). The desugar writes compiler-synthesised names
// (`__range_hi_1`, `__foreach_iter_1`) into the block the user wrote, so
// `-fmt -w` made them permanent and changed what is in scope there.
//
// Idempotence is the wrong property to test for this: format → parse →
// format is a fixed point on the leaked desugar too, because the second pass
// has nothing left to desugar. What has to hold is source preservation, so
// each case asserts the formatted text against the input.
func TestFormatForEachFormsKeepTheirSugar(t *testing.T) {
	for _, tc := range []struct{ name, loop string }{
		{"range", "for i in 0..4 {\n    t = t + i;\n  }"},
		{"range-inclusive", "for i in 0..=4 {\n    t = t + i;\n  }"},
		{"range-call-bound", "for i in lo()..hi() {\n    t = t + i;\n  }"},
		{"array", "for x in a {\n    t = t + x;\n  }"},
		{"labelled-array", "each: for x in a {\n    break each;\n  }"},
		{"nested", "for x in a {\n    for y in a {\n      t = t + x * y;\n    }\n  }"},
	} {
		src := "function lo(): i32 {\n  return 0;\n}\n\nfunction hi(): i32 {\n  return 4;\n}\n\nfunction f(a: i32[]): i32 {\n  var t: i32 = 0;\n  " +
			tc.loop + "\n  return t;\n}\n"
		if got := formatSrc(t, src); got != src {
			t.Errorf("%s: formatted output differs from source\n--- got ---\n%s\n--- want ---\n%s", tc.name, got, src)
		}
	}
}

// The map form binds two names through one iterator, so losing the sugar
// leaves `__foreach_iter_1.key()` / `.value()` calls in the source.
func TestFormatMapForEachKeepsItsSugar(t *testing.T) {
	src := `function f(m: map[string, i32]): i32 {
  var t: i32 = 0;
  for (k, v) in m {
    t = t + v + k.len();
  }
  return t;
}
`
	if got := formatSrc(t, src); got != src {
		t.Errorf("formatted output differs from source\n--- got ---\n%s\n--- want ---\n%s", got, src)
	}
}

// A loop label and the `break` / `continue` that target it are the same
// fact written twice; dropping either half silently retargets the jump to
// the innermost loop.
func TestFormatKeepsLoopLabels(t *testing.T) {
	src := `function f(a: i32[]): i32 {
  var t: i32 = 0;
  outer: while (t < 10) {
    inner: for (var i: i32 = 0; i < 3; i = i + 1) {
      if (i == 2) {
        continue outer;
      }
      break inner;
    }
    t = t + 1;
  }
  spin: loop {
    break spin;
  }
  return t;
}
`
	if got := formatSrc(t, src); got != src {
		t.Errorf("formatted output differs from source\n--- got ---\n%s\n--- want ---\n%s", got, src)
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

// A single-expression `if`/`match` branch must stay byte-identical to
// the pre-block-expr formatting (`{ a }` / bare arm body), and a
// block-expression branch (`{ stmts; tail }`) renders compactly without
// double-bracing.
func TestFormatBlockExprBranches(t *testing.T) {
	// Single-expr `if`-branch — unchanged.
	if got := formatSrc(t, `function f(a: i32, b: i32): i32 { return if (a < b) { a } else { b }; }`); !strings.Contains(got, "if (a < b) { a } else { b }") {
		t.Errorf("single-expr if-branch changed:\n%s", got)
	}
	// Block-expr `if`-branch — leading stmt + tail, no double braces.
	if got := formatSrc(t, `function f(e: i32): i32 { return if (e > 0) { var k = e + 1; k } else { 0 }; }`); !strings.Contains(got, "if (e > 0) { var k = e + 1; k } else { 0 }") {
		t.Errorf("block-expr if-branch mis-rendered:\n%s", got)
	}
	// Block-expr `match`-arm body; bare wildcard arm unchanged.
	if got := formatSrc(t, `function f(tag: i32): i32 { return match (tag) { 0 => { var s = tag + 5; s }, _ => 99 }; }`); !strings.Contains(got, "0 => { var s = tag + 5; s }, _ => 99") {
		t.Errorf("block-expr match-arm mis-rendered:\n%s", got)
	}
}

// Drive-by regression: a literal-pattern arm in a `match`-EXPRESSION
// (`0 => …`, `"yes" => …`) must format its pattern. The formatter used
// to drop it, emitting `=> …`, which then failed to re-parse.
func TestFormatMatchExprLiteralArm(t *testing.T) {
	got := formatSrc(t, `function f(n: i32): i32 { return match (n) { 0 => 10, 1 => 20, _ => 30 }; }`)
	if !strings.Contains(got, "0 => 10, 1 => 20, _ => 30") {
		t.Errorf("literal-pattern match-expr arm mis-rendered:\n%s", got)
	}
	if _, err := parser.Parse(got); err != nil {
		t.Errorf("formatted literal-arm match-expr failed to reparse:\n%s\nerror: %v", got, err)
	}
}

// parse → Format → parse must round-trip the AST shape (modulo the
// known comments-and-blank-lines limitation). The check is "does
// the formatted output reparse without errors".
func TestFormatRoundTripsThroughParser(t *testing.T) {
	srcs := []string{
		`function f(): i32 { return 1 + 2 * 3; }`,
		`function f(a: i32, b: i32): i32 { return if (a < b) { a } else { b }; }`,
		// Block-expression branches (slice 1): leading statement + tail.
		`function f(e: i32): i32 { return if (e > 0) { var k = e + 1; k } else { 0 }; }`,
		`function f(tag: i32): i32 { return match (tag) { 0 => { var s = tag + 5; s }, _ => 99 }; }`,
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

// A `resource` declaration (with its `@import` binding) and `own R` /
// `borrow R` handle types round-trip through the formatter unchanged (P5 —
// docs/WIT-BRING-YOUR-OWN.md).
func TestFormatResourceHandle(t *testing.T) {
	src := `@import("wasi:io/poll@0.2.0", "pollable")
resource Pollable;

@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): own Pollable;

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: borrow Pollable): boolean;
`
	got := formatSrc(t, src)
	for _, want := range []string{
		"@import(\"wasi:io/poll@0.2.0\", \"pollable\")\nresource Pollable;",
		"function subscribe(ns: u64): own Pollable;",
		"function ready(h: borrow Pollable): boolean;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatted output missing %q:\n%s", want, got)
		}
	}
	if _, err := parser.Parse(got); err != nil {
		t.Errorf("formatted resource/handle output failed to reparse:\n%s\nerror: %v", got, err)
	}
}

// An `@export("iface", "wit-name")` function binding round-trips through the
// formatter unchanged (P6 — docs/WIT-BRING-YOUR-OWN.md).
func TestFormatExportAttr(t *testing.T) {
	got := formatSrc(t, `@export("wasi:cli/run@0.2.0", "run")
function run(): i32 { return 0; }
`)
	if !strings.Contains(got, "@export(\"wasi:cli/run@0.2.0\", \"run\")\nfunction run(): i32 {") {
		t.Errorf("formatted output missing @export binding:\n%s", got)
	}
	if _, err := parser.Parse(got); err != nil {
		t.Errorf("formatted @export output failed to reparse:\n%s\nerror: %v", got, err)
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

// A generic struct declaration keeps its `[T]` type-parameter list.
// Regression guard: the formatter used to drop the list entirely,
// turning `struct Set[T]` into a non-generic `struct Set` whose `T`
// field type no longer resolved (silently un-compilable output).
func TestFormatGenericStructRoundTrip(t *testing.T) {
	got := formatSrc(t, `struct Set[T] { xs: T[] }`)
	if !strings.Contains(got, "struct Set[T] { xs: T[] }") {
		t.Errorf("expected generic struct decl with [T]; got:\n%s", got)
	}
	if again := formatSrc(t, got); got != again {
		t.Errorf("format not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

// A free generic function keeps its post-name type-parameter list,
// including trait bounds and generic-trait bound arguments.
func TestFormatGenericFreeFunctionRoundTrip(t *testing.T) {
	for _, want := range []string{
		"function id[T](x: T): T",
		"function eq2[T: Eq + Display](a: T, b: T): boolean",
		"function conv[T: From[i32]](x: T): T",
	} {
		src := want + " { return x; }"
		got := formatSrc(t, src)
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; got:\n%s", want, got)
		}
		if again := formatSrc(t, got); got != again {
			t.Errorf("format not idempotent for %q:\nfirst:\n%s\nsecond:\n%s", want, got, again)
		}
	}
}

// A generic *method* spells its type parameters in leading position,
// before the receiver, so the receiver type can reference them. The
// formatter must keep the clause; dropping it leaves an unbounded `T`
// that fails to typecheck.
func TestFormatGenericMethodRoundTrip(t *testing.T) {
	src := `struct Box[T] { xs: T[] }
pub function [T: Eq] (b: Box[T]) has(x: T): boolean { return false; }`
	got := formatSrc(t, src)
	if !strings.Contains(got, "function [T: Eq] (b: Box[T]) has(x: T): boolean") {
		t.Errorf("expected leading-position method type params; got:\n%s", got)
	}
	if again := formatSrc(t, got); got != again {
		t.Errorf("format not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

// An `own` (consuming) receiver keeps its `own` modifier through a
// formatter pass — dropping it silently turned a consuming method into
// a borrowing one.
func TestFormatOwnReceiverRoundTrip(t *testing.T) {
	got := formatSrc(t, `function (own self: Box) drop(): void {}`)
	if !strings.Contains(got, "function (own self: Box) drop(): void") {
		t.Errorf("expected `own` receiver preserved; got:\n%s", got)
	}
	if again := formatSrc(t, got); got != again {
		t.Errorf("format not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

// A trait declaration round-trips: type params, supertraits,
// associated types, abstract signatures, and default-method bodies all
// survive a formatter pass. Regression guard — the formatter used to
// omit trait declarations entirely (they weren't in the emit loop).
func TestFormatTraitRoundTrip(t *testing.T) {
	src := `pub trait Ord[K]: Eq {
  type Item;
  function cmp(self: Self, other: Self): i32;
  function max(self: Self): i32 { return 0; }
}`
	got := formatSrc(t, src)
	for _, w := range []string{
		"pub trait Ord[K]: Eq {",
		"type Item;",
		"function cmp(self: Self, other: Self): i32;",
		"function max(self: Self): i32 {",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output missing %q; got:\n%s", w, got)
		}
	}
	if again := formatSrc(t, got); got != again {
		t.Errorf("format not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

// An impl block round-trips: trait impls, inherent impls, parametric
// impls, associated functions (no `self`), and associated-type
// bindings. Regression guard — impls were dropped entirely, and their
// methods leaked out as top-level receiver functions. `Self` renders as
// the concrete impl type (the desugared form), which re-parses to the
// same AST.
func TestFormatImplRoundTrip(t *testing.T) {
	src := `struct Box[T] { xs: T[] }
trait Maker { function make(): Self; }
impl Maker for Box[i32] { function make(): Self { return Box { xs: [] }; } }
impl[T] Box[T] { function size(self: Self): i32 { return self.xs.len(); } }`
	got := formatSrc(t, src)
	for _, w := range []string{
		"impl Maker for Box[i32] {",
		"function make(): Box[i32] {",        // assoc fn, Self -> concrete, no self param
		"impl[T] Box[T] {",                   // inherent parametric impl
		"function size(self: Box[T]): i32 {", // method: self re-inserted, params NOT respelled
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output missing %q; got:\n%s", w, got)
		}
	}
	// The impl methods must NOT leak out as bare top-level functions —
	// a top-level decl starts at column 0 (`\nfunction …`), an impl
	// method is indented two spaces (`\n  function …`).
	if strings.Contains(got, "\nfunction make(") || strings.Contains(got, "\nfunction size(") {
		t.Errorf("impl method leaked to top level:\n%s", got)
	}
	if again := formatSrc(t, got); got != again {
		t.Errorf("format not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

// An associated-type binding in an impl round-trips (`type Item = i32;`).
func TestFormatImplAssocTypeBinding(t *testing.T) {
	src := `struct C {}
trait Iter { type Item; function get(self: Self): i32; }
impl Iter for C { type Item = i32; function get(self: Self): i32 { return 0; } }`
	got := formatSrc(t, src)
	if !strings.Contains(got, "type Item = i32;") {
		t.Errorf("expected assoc-type binding; got:\n%s", got)
	}
	if again := formatSrc(t, got); got != again {
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
// The method-call shape needs its own `case *ast.Defer` arm in the
// statement printer's switch, or it is eaten silently, leaving an
// empty line where `defer r.close();` stood.
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

// Block-shaped `defer { … }` / `errdefer { … }` (#5153) round-trips: the
// action prints as a brace block with NO trailing `;` (so it re-parses), and
// the format is idempotent.
func TestFormatDeferBlockRoundTrip(t *testing.T) {
	srcs := []string{
		`function f(): void { var x = 0; defer { x = x + 1; } }`,
		`function f(): Result[i32, i32] { var x = 0; errdefer { x = x + 2; } return Ok(x); }`,
	}
	for _, src := range srcs {
		got := formatSrc(t, src)
		if !strings.Contains(got, "defer { ") {
			t.Errorf("block defer not formatted as a brace block for %q:\n%s", src, got)
		}
		if strings.Contains(got, "};") {
			t.Errorf("block defer emitted an invalid trailing `;` for %q:\n%s", src, got)
		}
		// The formatted output must itself re-parse.
		if _, err := parser.Parse(got); err != nil {
			t.Errorf("formatted block defer does not re-parse for %q:\n%s\nerr: %v", src, got, err)
		}
		if again := formatSrc(t, got); got != again {
			t.Errorf("format not idempotent for %q:\nfirst:\n%s\nsecond:\n%s", src, got, again)
		}
	}
}

// `errdefer` statements round-trip through the formatter and keep the
// `errdefer` keyword (not silently rewritten to `defer`). The printer
// branches on ast.Defer.OnError.
func TestFormatErrDeferRoundTrip(t *testing.T) {
	srcs := []string{
		`function f(): Result[i32, i32] { errdefer cleanup(); return Ok(0); }`,
		`function f(r: Reader): Result[i32, i32] {
errdefer r.close();
defer log();
return Ok(0);
}`,
	}
	for _, src := range srcs {
		got := formatSrc(t, src)
		if !strings.Contains(got, "errdefer ") {
			t.Errorf("`errdefer` keyword stripped from output for input %q:\n%s", src, got)
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

// Imports round-trip through the formatter. A Format loop that walks
// only structs / enums / unions / consts / funcs drops them silently,
// so `fern -fmt -w` strips every import line and the fmt-check CI gate
// fails.
func TestFormatImportsRoundTrip(t *testing.T) {
	got := formatSrc(t, `import "std/string";
import "std/i32";
function main(): i32 { return 0; }`)
	for _, want := range []string{
		`import "std/string";`,
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
// formatter, which needs a `formatUnionDecl` path or they are dropped
// silently. Members preserved in source
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

// checkRejects reports whether the checker rejects src.
func checkRejects(t *testing.T, src string) bool {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = checker.Check(prog)
	return err != nil
}

// Format must preserve `@must_consume` on structs and enums. Dropping it was
// not cosmetic: the attribute is what E067's obligation walk keys on, so a
// formatted file type-checked CLEAN where the original was rejected — the
// formatter silently disarmed the analysis the annotation exists to drive.
// Asserted through the CHECKER rather than on the text, because the text is
// only a proxy for the property that matters.
func TestFormatMustConsumeAttr(t *testing.T) {
	for _, src := range []string{
		"@must_consume struct Res { code: i32 } function main(): i32 { var r: Res = Res { code: 7 }; return r.code; }",
		"@must_consume enum R { Ok, Bad } function main(): i32 { var r: R = Ok; return 0; }",
	} {
		out := formatSrc(t, src)
		if !strings.Contains(out, "@must_consume") {
			t.Errorf("formatted output dropped @must_consume:\n%s", out)
		}
		// The original is rejected; so must the formatted form be. Asserted
		// through the checker because the text is only a proxy for that.
		if !checkRejects(t, src) {
			t.Fatalf("fixture does not trip E067 to begin with, so the round trip proves nothing:\n%s", src)
		}
		if !checkRejects(t, out) {
			t.Errorf("the formatted file type-checks clean — formatting disarmed E067:\n%s", out)
		}
	}
}

// `todo;` / `todo("msg");` desugars in the parser to a `loop { eprint;
// exit(101); }` stub, but — unlike `assert`, which formats as its desugared
// form — the formatter must re-print the todo SUGAR: the marker is a
// remaining-work inventory (`-check` warns per site) that `fern -fmt` must
// not erase. Pinned here: both forms survive, the message expression is
// reproduced verbatim, and formatting is idempotent.
func TestFormatTodoRoundTrip(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`function f(): i32 { todo; }`, "todo;"},
		{`function f(): i32 { todo("port the wide-K case"); }`, `todo("port the wide-K case");`},
		{`function f(): i32 { todo(); }`, "todo;"},
	}
	for _, c := range cases {
		got := formatSrc(t, c.src)
		if !strings.Contains(got, c.want) {
			t.Errorf("formatted output for %q lost the todo sugar (want %q):\n%s", c.src, c.want, got)
		}
		if strings.Contains(got, "loop") || strings.Contains(got, "eprint") {
			t.Errorf("formatted output for %q leaked the desugared body:\n%s", c.src, got)
		}
		again := formatSrc(t, got)
		if got != again {
			t.Errorf("format not idempotent for %q:\nfirst:\n%s\nsecond:\n%s", c.src, got, again)
		}
	}
}

// The pipe topic placeholder (`x |> f(a, _)`) must round-trip: the
// formatter re-renders the LHS from the substituted slot (Call.PipeHole)
// and puts the `_` back, instead of printing the desugared prepended-arg
// form. Nested holes and the plain prepended form must survive alongside.
func TestFormatPipeHoleRoundTrip(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`function main(): i32 { var x: i32 = 3; return x |> sub(10, _); }`, `x |> sub(10, _)`},
		{`function main(): i32 { var x: i32 = 3; return x |> sub(_, 1); }`, `x |> sub(_, 1)`},
		{`function main(): i32 { var x: i32 = 3; return 20 |> sub(_, x |> sub(5, _)); }`, `20 |> sub(_, x |> sub(5, _))`},
		// No hole: the existing prepended rendering is unchanged.
		{`function main(): i32 { var x: i32 = 3; return x |> sub(10); }`, `x |> sub(10)`},
	}
	for _, c := range cases {
		got := formatSrc(t, c.src)
		if !strings.Contains(got, c.want) {
			t.Errorf("formatted output for %q lost the pipe form (want %q):\n%s", c.src, c.want, got)
		}
		again := formatSrc(t, got)
		if got != again {
			t.Errorf("format not idempotent for %q:\nfirst:\n%s\nsecond:\n%s", c.src, got, again)
		}
	}
}

// A struct destructure binds BY FIELD NAME. The formatter used to render
// every *ast.Destructure as the positional tuple form, so `let Point { x:
// a, y } = p;` came back out as `let (a, y) = p;` — a different program
// (positional binding, rename lost), and one that fails outright on a
// partial or reordered bind. Both destructure modes must survive the
// round trip, including the parameter-pattern desugar that produces a
// struct-mode Destructure in the body prelude.
func TestFormatStructDestructureKeepsFieldNames(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			name: "rename",
			src:  "struct Point { x: i32, y: i32 }\nfunction f(p: Point): i32 { let Point { x: a, y } = p; return a + y; }",
			want: "let Point { x: a, y } = p;",
		},
		{
			name: "partial",
			src:  "struct Point { x: i32, y: i32 }\nfunction f(p: Point): i32 { let Point { y } = p; return y; }",
			want: "let Point { y } = p;",
		},
		{
			name: "param_pattern_prelude",
			src:  "struct Point { x: i32, y: i32 }\nfunction f(Point { x: a, y }: Point): i32 { return a + y; }",
			want: "let Point { x: a, y } = __ptuple_",
		},
		{
			name: "tuple_mode_unchanged",
			src:  "function f(t: (i32, i32)): i32 { let (a, b) = t; return a + b; }",
			want: "let (a, b) = t;",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSrc(t, tc.src)
			if !strings.Contains(got, tc.want) {
				t.Errorf("got:\n%s\nwant it to contain %q", got, tc.want)
			}
			// Re-parse + re-format: the output must be a valid program that
			// formats to itself, which is what catches a mode swap.
			if again := formatSrc(t, got); again != got {
				t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
			}
		})
	}
}

// A `Map { … }` literal had no printer case at all, so Format wrote the field
// name and its `: ` and then nothing — `Layer { writes: , … }`, source that
// cannot re-parse. `fern -fmt -w` on such a file destroyed it (#6803).
func TestFormatMapLiteral(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"empty", "struct S { m: Map[string, i32] }\nfunction f(): S { return S { m: Map { } }; }", "S { m: Map { } }"},
		{"entries", `function f(): Map[string, i32] { return Map { "a": 1, "b": 2 }; }`, `Map { "a": 1, "b": 2 }`},
		{"expr_values", "function f(k: string, v: i32): Map[string, i32] { return Map { k: v + 1 }; }", "Map { k: v + 1 }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSrc(t, tc.src)
			if !strings.Contains(got, tc.want) {
				t.Errorf("got:\n%s\nwant it to contain %q", got, tc.want)
			}
			if _, err := parser.Parse(got); err != nil {
				t.Errorf("formatted output does not re-parse: %v\n%s", err, got)
			}
			if again := formatSrc(t, got); again != got {
				t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
			}
		})
	}
}

// The parser renames a `_` binding to an internal `__discard_<line>_<col>_<n>`
// so reading it back is an undefined-identifier error. Format printed that
// name, writing a compiler-internal identifier — position suffix included —
// back into the user's file (#6803).
func TestFormatKeepsDiscardBindingAsUnderscore(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"tuple_destructure", "function t(): (i32, i32) { return (1, 2); }\nfunction f(): i32 { let (_, x) = t(); return x; }", "let (_, x) = t();"},
		{"both_discarded", "function t(): (i32, i32) { return (1, 2); }\nfunction f(): i32 { let (_, _) = t(); return 0; }", "let (_, _) = t();"},
		{"var", "function f(): i32 { var _ = 1; return 0; }", "var _ = 1;"},
		{"param", "function f(_: i32): i32 { return 0; }", "function f(_: i32): i32 {"},
		{"lambda_param", "function f(): i32 { var g = function(_: i32): i32 { return 0; }; return g(1); }", "function(_: i32): i32"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSrc(t, tc.src)
			if strings.Contains(got, "__discard_") {
				t.Errorf("formatted output leaks the parser's synthesised discard name:\n%s", got)
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("got:\n%s\nwant it to contain %q", got, tc.want)
			}
			if again := formatSrc(t, got); again != got {
				t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
			}
		})
	}
}

// A literal's base is part of what the author wrote — the arm64 and x86
// encoders spell every literal as the instruction encoding it is — and Format
// rewrote `0xd2800000` to `3531603968` (#6803).
func TestFormatKeepsHexLiteralBase(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"lowercase", "function f(): i32 { return 0xdead; }", "return 0xdead;"},
		{"uppercase_digits", "function f(): u32 { return 0xFFFFFFFF as u32; }", "0xFFFFFFFF as u32"},
		{"typed_suffix", "function f(): i64 { return 0xffi64; }", "return 0xffi64;"},
		{"decimal_untouched", "function f(): i32 { return 4095; }", "return 4095;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSrc(t, tc.src)
			if !strings.Contains(got, tc.want) {
				t.Errorf("got:\n%s\nwant it to contain %q", got, tc.want)
			}
			if again := formatSrc(t, got); again != got {
				t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
			}
		})
	}
}

// An arrow lambda parses to the same node as `function(…) { … }`, so Format
// re-emitted it in that form and had to supply a return type it does not know:
// `() => e` became `function(): void { return e; }`, asserting `void` over an
// expression that has a value (#6803).
func TestFormatKeepsArrowLambda(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"no_params", "function g(f: () => i32): i32 { return f(); }\nfunction main(): i32 { return g(() => 7); }", "g(() => 7)"},
		{"one_param", "function g(f: (i32) => i32): i32 { return f(1); }\nfunction main(): i32 { return g((x: i32) => x + 1); }", "g((x: i32) => x + 1)"},
		{"annotated_return", "function g(f: (i32) => i32): i32 { return f(1); }\nfunction main(): i32 { return g((x: i32): i32 => x + 1); }", "g((x: i32): i32 => x + 1)"},
		{"function_form_unchanged", "function g(f: (i32) => i32): i32 { return f(1); }\nfunction main(): i32 { return g(function(x: i32): i32 { return x + 1; }); }", "function(x: i32): i32 { return x + 1; }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSrc(t, tc.src)
			if strings.Contains(got, "): void {") {
				t.Errorf("formatted output invented a void return type:\n%s", got)
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("got:\n%s\nwant it to contain %q", got, tc.want)
			}
			// The checker is the assertion that matters: `void` over an
			// expression that has a value is a type error, so a clean check
			// is what says the round trip preserved the program.
			if checkRejects(t, got) {
				t.Errorf("formatted output no longer type-checks:\n%s", got)
			}
			if again := formatSrc(t, got); again != got {
				t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
			}
		})
	}
}
