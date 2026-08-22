package e2eselfhost

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// The self-host feature census (#6993). `examples/self_host/*.fern` is validated
// largely by compiling itself, so a language feature the self-host's own sources
// do not use gets no fixpoint coverage at all — the gate can only prove what the
// code already exercises. That makes "what does the self-host actually use?" a
// number that steers decisions: it is what docs/SELFHOST-LANGUAGE-FRICTION.md §1
// is built from, and what says whether a new e2eselfhost fixture covers a real
// gap or a hypothetical one. A measurement that steers a decision has to be
// reproducible and has to fail when the thing it measured moves.
//
// Counting has to strip first. The self-host embeds whole test programs as
// string literals and discusses its own syntax in prose comments, so a raw grep
// for `=>` or `Map[` counts the compiler talking ABOUT a construct as one that
// uses it — on the `as` row the raw and stripped counts differ by a factor of
// five, which is how three successive hand-measurements of this census
// disagreed with each other in both directions. The strip is the
// correctness-critical half: TestStripFernLiterals pins it against the cases
// that break a naive one, and the census re-proves it on the real corpus before
// counting anything.

// stripFernLiterals blanks `//` comments, string and f-string literals, and char
// and byte literals, leaving the code text the census counts over. Line
// structure is preserved, so a hit still reports a usable line number.
//
// Every Fern literal is line-bounded — a newline inside a string, f-string or
// char literal is a lex error (internal/lexer) — so the strip runs per line and
// an unterminated literal can never swallow the rest of the file.
func stripFernLiterals(src string) string {
	lines := strings.Split(src, "\n")
	for i, ln := range lines {
		lines[i] = stripFernLine(ln)
	}
	return strings.Join(lines, "\n")
}

func stripFernLine(line string) string {
	var b strings.Builder
	var last byte
	emit := func(s string) {
		b.WriteString(s)
		last = s[len(s)-1]
	}
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == '/' && i+1 < len(line) && line[i+1] == '/':
			return b.String()
		case c == '"':
			emit(`""`)
			i = skipFernString(line, i)
		case c == 'f' && i+1 < len(line) && line[i+1] == '"' && !identCont(last):
			// `f"…{expr}…"`. The interpolants are real code, but nothing
			// the census counts is ever spelled inside one, so dropping
			// them keeps the strip to a single rule: a literal contributes
			// nothing.
			emit(`""`)
			i = skipFernFString(line, i+1)
		case c == '\'':
			if end := skipFernChar(line, i); end > 0 {
				emit(`''`)
				i = end
			} else {
				emit(line[i : i+1])
				i++
			}
		default:
			emit(line[i : i+1])
			i++
		}
	}
	return b.String()
}

func identCont(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// skipFernString returns the index just past the string literal opening at i, or
// len(line) when the literal does not close on this line.
func skipFernString(line string, i int) int {
	for i++; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(line)
}

// skipFernFString is skipFernString for an f-string body, whose interpolants nest
// braces and may hold a string of their own, so the closing quote is only the one
// seen at brace depth zero.
func skipFernFString(line string, i int) int {
	depth := 0
	for i++; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++
		case '{':
			if depth == 0 && i+1 < len(line) && line[i+1] == '{' {
				i++ // `{{` is a literal brace, not an interpolant
				continue
			}
			depth++
		case '}':
			if depth == 0 && i+1 < len(line) && line[i+1] == '}' {
				i++ // `}}` is a literal brace
				continue
			}
			if depth > 0 {
				depth--
			}
		case '"':
			if depth == 0 {
				return i + 1
			}
			i = skipFernString(line, i) - 1
		}
	}
	return len(line)
}

// skipFernChar returns the index just past the char or byte literal opening at i,
// or -1 when what follows is not a literal — which is how an apostrophe that
// reaches code text stays one character instead of swallowing the line up to the
// next quote.
func skipFernChar(line string, i int) int {
	j := i + 1
	if j < len(line) && line[j] == '\\' {
		j += 2
	} else {
		_, sz := utf8.DecodeRuneInString(line[j:])
		j += sz
	}
	if j < len(line) && line[j] == '\'' {
		return j + 1
	}
	return -1
}

// strippedSource is one self-host module with its literals gone.
type strippedSource struct {
	name  string
	lines []string
}

func selfHostStripped(t *testing.T) []strippedSource {
	t.Helper()
	paths, err := filepath.Glob(langSrcAbs(t, filepath.Join("examples", "self_host", "*.fern")))
	if err != nil {
		t.Fatalf("globbing self-host sources: %v", err)
	}
	if len(paths) < 90 {
		t.Fatalf("found %d self-host modules, expected the full set — a shrunken sweep passes every floor below by vacuity", len(paths))
	}
	sort.Strings(paths)
	out := make([]strippedSource, 0, len(paths))
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		out = append(out, strippedSource{
			name:  filepath.Base(p),
			lines: strings.Split(stripFernLiterals(string(src)), "\n"),
		})
	}
	return out
}

// censusHit is one match, kept with its location so a row that moves says where.
type censusHit struct {
	file string
	line int
	text string
}

func (h censusHit) String() string {
	return fmt.Sprintf("%s:%d: %s", h.file, h.line, strings.TrimSpace(h.text))
}

// census maps a row name to every site that row counted.
type census map[string][]censusHit

func (c census) n(name string) int { return len(c[name]) }

// where names the sites of a row, capped so a 5,000-hit row stays readable.
func (c census) where(name string) string {
	hits := c[name]
	const shown = 12
	parts := make([]string, 0, shown+1)
	for i, h := range hits {
		if i == shown {
			parts = append(parts, fmt.Sprintf("… and %d more", len(hits)-shown))
			break
		}
		parts = append(parts, h.String())
	}
	return strings.Join(parts, "\n        ")
}

// censusRow is one line of the table. Rows carrying no assertion are still
// reported: docs/SELFHOST-LANGUAGE-FRICTION.md quotes them, and `-v` on this test
// is how they are re-measured.
type censusRow struct {
	name string
	pat  string
	// must is a literal substring every match of pat contains. It is a
	// prefilter — the census is 175k lines and a `\b`-anchored regex over all
	// of them costs ~140ms each — and getting one wrong drops hits, which the
	// pins below catch.
	must string
}

var censusRows = []censusRow{
	// The language features. Each is pinned below: the fixpoint's coverage of
	// the feature is exactly these sites and nothing else.
	{"generic functions", `\bfunction\s+[A-Za-z_][A-Za-z0-9_]*\s*\[`, "function"},
	{"generic structs", `\bstruct\s+[A-Za-z_][A-Za-z0-9_]*\s*\[`, "struct"},
	// An arrow lambda's parameter list is annotated and holds no nested
	// parens; a fn-TYPE annotation like `(parser.Expr, T) => T` has neither,
	// which is what separates the two spellings textually.
	{"arrow lambdas", `(^|[^A-Za-z0-9_])\(\s*[A-Za-z_][A-Za-z0-9_]*\s*:[^()]*\)\s*=>`, "=>"},
	// `function(x: T): R {` as an expression. The `[:{]` is what tells it from
	// a method declaration, whose parameter list is a RECEIVER and is followed
	// by the method name.
	{"anonymous function exprs", `\bfunction\s*\([^()]*\)\s*[:{]`, "function"},
	{"nested named fns", `^[ \t]+(pub\s+)?function\s+[A-Za-z_]`, "function"},
	{"for..in loops", `\bfor\s+[A-Za-z_][A-Za-z0-9_]*\s+in\s`, "for"},
	// Every `?` token. There are none at all, so nothing here yet needs to tell
	// the try operator from an optional-type suffix.
	{"try op", `\?`, "?"},
	{"Map type spellings", `\bMap\s*\[`, "Map"},
	{"astwalk call sites", `\bastwalk\s*\.`, "astwalk"},

	// The dialect the self-host writes instead. Ceilings, or context for them.
	{"wildcard match arms", `(^|[^A-Za-z0-9_])_\s*=>`, "=>"},
	{"arrow tokens", `=>`, "=>"},
	{"while loops", `\bwhile\s*\(`, "while"},
	{"as casts", `\bas\b`, "as"},
	{"minus-one sentinel returns", `\breturn\s+0\s*-\s*1\b`, "return"},
	{"method decls", `\bfunction\s*\([^()]*\)\s*[A-Za-z_]`, "function"},
	{"annotated var decls", `\bvar\s+[A-Za-z_][A-Za-z0-9_]*\s*:`, "var"},
	{"inferred var decls", `\bvar\s+[A-Za-z_][A-Za-z0-9_]*\s*=`, "var"},
}

// incrementRe is the hand-written `x = x + 1` the index-loop dialect is built
// from. It needs the two names compared, which RE2 has no backreference for, so
// it is counted apart from the table.
var incrementRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*=\s*([A-Za-z_][A-Za-z0-9_]*)\s*\+\s*1\b`)

func takeCensus(t *testing.T, srcs []strippedSource) census {
	t.Helper()
	c := census{}
	for _, r := range censusRows {
		re := regexp.MustCompile(r.pat)
		for _, s := range srcs {
			for i, ln := range s.lines {
				if !strings.Contains(ln, r.must) {
					continue
				}
				for range re.FindAllStringIndex(ln, -1) {
					c[r.name] = append(c[r.name], censusHit{file: s.name, line: i + 1, text: ln})
				}
			}
		}
	}
	for _, s := range srcs {
		for i, ln := range s.lines {
			if !strings.Contains(ln, "+") {
				continue
			}
			for _, m := range incrementRe.FindAllStringSubmatch(ln, -1) {
				if m[1] == m[2] {
					c["increment by one"] = append(c["increment by one"], censusHit{file: s.name, line: i + 1, text: ln})
				}
			}
		}
	}
	return c
}

func logCensus(t *testing.T, srcs []strippedSource, c census) {
	t.Helper()
	names := make([]string, 0, len(c))
	for _, r := range censusRows {
		names = append(names, r.name)
	}
	names = append(names, "increment by one")
	t.Logf("self-host feature census over %d modules, literals stripped:", len(srcs))
	for _, name := range names {
		mods := map[string]bool{}
		for _, h := range c[name] {
			mods[h.file] = true
		}
		t.Logf("  %-26s %6d  in %d modules", name, c.n(name), len(mods))
	}
}

// pinned asserts a row has not moved in either direction. The feature rows are
// small enough that any move is worth a look, and pinning them is what keeps the
// doc's table honest — a floor alone lets the number drift up unnoticed, which is
// how the disagreeing hand-counts arose.
func pinned(t *testing.T, c census, name string, want int, why string) {
	t.Helper()
	if got := c.n(name); got != want {
		t.Errorf("%s: census counts %d, pinned at %d.\n"+
			"    %s\n"+
			"    Re-measure with `go test ./internal/e2eselfhost/ -run TestSelfHostFeatureCensus -v`, then move BOTH this number and the row in docs/SELFHOST-LANGUAGE-FRICTION.md §1.\n"+
			"    sites:\n        %s", name, got, want, why, c.where(name))
	}
}

func atLeast(t *testing.T, c census, name string, want int, why string) {
	t.Helper()
	if got := c.n(name); got < want {
		t.Errorf("%s: census counts %d, floor %d.\n    %s\n    sites:\n        %s", name, got, want, why, c.where(name))
	}
}

func atMost(t *testing.T, c census, name string, limit, measured int, why string) {
	t.Helper()
	if got := c.n(name); got > limit {
		t.Errorf("%s: census counts %d, ceiling %d (measured %d, plus deliberate headroom).\n"+
			"    %s\n"+
			"    Re-measure with `go test ./internal/e2eselfhost/ -run TestSelfHostFeatureCensus -v`. Then either convert the new call sites, or — if the growth is legitimate — move the ceiling and say in the commit message what added the sites.\n"+
			"    sites:\n        %s", name, got, limit, measured, why, c.where(name))
	}
}

func TestSelfHostFeatureCensus(t *testing.T) {
	srcs := selfHostStripped(t)
	assertNoLiteralResidue(t, srcs)
	c := takeCensus(t, srcs)
	logCensus(t, srcs, c)

	// The features. These sites are the ENTIRE fixpoint coverage of each row:
	// delete them and the self-host stops exercising the feature, whatever the
	// e2eselfhost fixtures do.
	pinned(t, c, "generic functions", 8,
		"Every one is astwalk's fold spine. It is the only generic code the self-host compiles, so it is the only monomorphisation the fixpoint exercises.")
	pinned(t, c, "generic structs", 0,
		"The self-host declares no generic struct, so nothing on the fixpoint path monomorphises a generic TYPE — only generic functions.")
	pinned(t, c, "arrow lambdas", 2,
		"Both are astwalk's no-op statement visitors. They are the whole of the self-host's arrow-lambda coverage.")
	pinned(t, c, "anonymous function exprs", 4,
		"`function(x: T): R { … }` in expression position — astwalk's splice, checker's diag fold, and two parser rewriters. All capture, so these plus the nested named fns are the self-host's only closures.")
	pinned(t, c, "nested named fns", 4,
		"All four are visitors closing over their enclosing function's locals — the capturing-closure spelling astwalk's consumers use.")
	pinned(t, c, "for..in loops", 326,
		"311 in checker.fern and 15 in visibility.fern. Every other loop in the compiler is still a hand-indexed `while`, so these two modules are the whole of the fixpoint's for..in coverage.")
	pinned(t, c, "try op", 0,
		"The self-host propagates errors by hand, so `?` has NO fixpoint coverage. A rise here is good news and means this row and the doc's have to move.")
	pinned(t, c, "Map type spellings", 11,
		"irverify's NameIndex, wasm_ir's call set, and builtins' mirror of std/json's JObject payload. The only hash map the self-host compiles.")

	// astwalk adoption is the metric the walker migration moves, so it is a
	// floor rather than a pin: it is meant to climb, and pinning it would fight
	// the migration it measures.
	atLeast(t, c, "astwalk call sites", 85,
		"Hand-written AST walkers collapsing onto the shared fold spine is what this counts. It should only climb; a fall means a consumer went back to spelling its own traversal.")

	// The ratchet. These two are what the self-host writes INSTEAD of the
	// features above, and both are only supposed to fall. The ceilings carry
	// ~10% headroom over the measurement — enough for a normal PR's worth of new
	// arms or loops without a red build, tight enough that a new pass written
	// wholesale in the old dialect trips it.
	atMost(t, c, "wildcard match arms", 2800, 2563,
		"A `_ =>` arm is a match that does not enumerate its cases, so a new parser node added later is silently swallowed instead of caught. The fold spine exists to remove them.")
	atMost(t, c, "increment by one", 5200, 4728,
		"Every `x = x + 1` is one hand-written index loop that `for x in xs` would carry. This is the dialect the compiler is written in, and the count is the size of the migration left.")
}

// assertNoLiteralResidue re-proves the strip on the real corpus before anything
// is counted over it: nothing that survives may contain a `//`, or a string or
// char quote outside the empty pair the strip leaves behind. A mis-scanned
// literal shows up here as residue rather than silently as a wrong count.
func assertNoLiteralResidue(t *testing.T, srcs []strippedSource) {
	t.Helper()
	for _, s := range srcs {
		for i, ln := range s.lines {
			bad := ""
			switch {
			case strings.Contains(ln, "//"):
				bad = "comment"
			case strings.Contains(strings.ReplaceAll(ln, `""`, ""), `"`):
				bad = "string"
			case strings.Contains(strings.ReplaceAll(ln, "''", ""), "'"):
				bad = "char"
			}
			if bad != "" {
				t.Errorf("%s:%d: %s literal survived the strip, so every count below it is unsound: %s", s.name, i+1, bad, ln)
			}
		}
	}
}

func TestStripFernLiterals(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"plain code", `var i: i32 = 0;`, `var i: i32 = 0;`},
		{"line comment", `var i: i32 = 0; // count _ => arms`, `var i: i32 = 0; `},
		{"whole-line comment", `// for x in xs { }`, ``},
		{"string", `emit("for x in xs");`, `emit("");`},
		{"comment marker inside string", `emit("http://x _ => y");`, `emit("");`},
		{"escaped quote in string", `emit("he said \"x = x + 1\" ok");`, `emit("");`},
		{"string ending in escaped backslash", `emit("c:\\");`, `emit("");`},
		{"apostrophe inside string", `emit("don't _ => x");`, `emit("");`},
		{"quote inside char literal", `if (c == '"') { }`, `if (c == '') { }`},
		{"escaped quote char literal", `if (c == '\'') { }`, `if (c == '') { }`},
		{"escaped backslash char literal", `if (c == '\\') { }`, `if (c == '') { }`},
		{"byte literal", `if (b == b'\n') { }`, `if (b == b'') { }`},
		{"multi-byte char literal", `if (c == '∃') { }`, `if (c == '') { }`},
		{"f-string", `out = f"i={i} _ => x";`, `out = "";`},
		{"f-string with nested string", `out = f"{join(xs, ", ")} _ => x";`, `out = "";`},
		{"f-string with brace escapes", `out = f"{{ _ => }} {i}";`, `out = "";`},
		{"identifier ending in f", `var buf: string = "x";`, `var buf: string = "";`},
		// An apostrophe that reaches code text is not a literal, and must stay
		// one character rather than eating the line up to the next quote.
		{"lone apostrophe", `a ' b _ => c`, `a ' b _ => c`},
		// An unterminated literal is a lex error, not something the strip is
		// entitled to carry into the next line.
		{"unterminated string", "emit(\"oops\nvar i: i32 = 0;", "emit(\"\"\nvar i: i32 = 0;"},
		{"unterminated comment-free f-string", "out = f\"{a\nvar i: i32 = 0;", "out = \"\"\nvar i: i32 = 0;"},
		{"line count preserved", "a\n// b\nc", "a\n\nc"},
		{"comment then char literal next line", "// don't\nif (c == 'x') { }", "\nif (c == '') { }"},
		{"string then comment", `emit("a"); // don't count "b"`, `emit(""); `},
		{"two strings on a line", `f("a", "b");`, `f("", "");`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripFernLiterals(tc.src); got != tc.want {
				t.Errorf("stripFernLiterals(%q)\n = %q\nwant %q", tc.src, got, tc.want)
			}
		})
	}
}
