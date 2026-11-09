package e2ecompiler

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

func skipFernFString(line string, i int) int {
	depth := 0
	for i++; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++
		case '{':
			if depth == 0 && i+1 < len(line) && line[i+1] == '{' {
				i++
				continue
			}
			depth++
		case '}':
			if depth == 0 && i+1 < len(line) && line[i+1] == '}' {
				i++
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

type strippedSource struct {
	name  string
	lines []string
}

func selfHostStripped(t *testing.T) []strippedSource {
	t.Helper()
	paths, err := filepath.Glob(langSrcAbs(t, filepath.Join("compiler", "*.fern")))
	inDrivers, _ := filepath.Glob(langSrcAbs(t, filepath.Join("compiler", "drivers", "*.fern")))
	paths = append(paths, inDrivers...)
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

type censusHit struct {
	file string
	line int
	text string
}

func (h censusHit) String() string {
	return fmt.Sprintf("%s:%d: %s", h.file, h.line, strings.TrimSpace(h.text))
}

type census map[string][]censusHit

func (c census) n(name string) int { return len(c[name]) }

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

type censusRow struct {
	name string
	pat  string
	must string
}

var censusRows = []censusRow{
	{"generic functions", `\bfunction\s+[A-Za-z_][A-Za-z0-9_]*\s*\[`, "function"},
	{"generic structs", `\bstruct\s+[A-Za-z_][A-Za-z0-9_]*\s*\[`, "struct"},
	{"arrow lambdas", `(^|[^A-Za-z0-9_])\(\s*[A-Za-z_][A-Za-z0-9_]*\s*:[^()]*\)\s*(:\s*[^()=]+)?=>`, "=>"},
	{"nested named fns", `^[ \t]+(pub\s+)?function\s+[A-Za-z_]`, "function"},
	{"for..in loops", `\bfor\s+[A-Za-z_][A-Za-z0-9_]*\s+in\s`, "for"},
	{"try op", `\?`, "?"},
	{"Map type spellings", `\bMap\s*\[`, "Map"},
	{"astwalk call sites", `\bastwalk\s*\.`, "astwalk"},

	{"wildcard match arms", `(^|[^A-Za-z0-9_])_\s*=>`, "=>"},
	{"arrow tokens", `=>`, "=>"},
	{"while loops", `\bwhile\s*\(`, "while"},
	{"as casts", `\bas\b`, "as"},
	{"minus-one sentinel returns", `\breturn\s+0\s*-\s*1\b`, "return"},
	{"method decls", `\bfunction\s*\([^()]*\)\s*[A-Za-z_]`, "function"},
	{"annotated let decls", `\blet\s+[A-Za-z_][A-Za-z0-9_]*\s*:`, "let"},
	{"inferred let decls", `\blet\s+[A-Za-z_][A-Za-z0-9_]*\s*=`, "let"},
}

// incrementRe matches `x = x + 1` and `x += 1`. Group 2 is empty for the second form.
var incrementRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*(?:\+=\s*1\b|=\s*([A-Za-z_][A-Za-z0-9_]*)\s*\+\s*1\b)`)

func incrementsByOne(ln string) int {
	n := 0
	for _, m := range incrementRe.FindAllStringSubmatch(ln, -1) {
		if m[2] == "" || m[1] == m[2] {
			n++
		}
	}
	return n
}

var methodContainerRe = regexp.MustCompile(`^\s*(pub\s+)?(impl|trait)\b`)

func methodDeclarationLines(s strippedSource) map[int]bool {
	methods := map[int]bool{}
	depth := 0
	var containers []int
	pending := false
	for i, line := range s.lines {
		if methodContainerRe.MatchString(line) {
			pending = true
		}
		if len(containers) > 0 && depth == containers[len(containers)-1] {
			methods[i] = true
		}
		for _, c := range line {
			switch c {
			case '{':
				depth++
				if pending {
					containers = append(containers, depth)
					pending = false
				}
			case '}':
				if len(containers) > 0 && depth == containers[len(containers)-1] {
					containers = containers[:len(containers)-1]
				}
				depth--
			}
		}
	}
	return methods
}

func takeCensus(t *testing.T, srcs []strippedSource) census {
	t.Helper()
	c := census{}
	methods := make([]map[int]bool, len(srcs))
	for i, s := range srcs {
		methods[i] = methodDeclarationLines(s)
	}
	for _, r := range censusRows {
		re := regexp.MustCompile(r.pat)
		for si, s := range srcs {
			for i, ln := range s.lines {
				if !strings.Contains(ln, r.must) {
					continue
				}
				for range re.FindAllStringIndex(ln, -1) {
					name := r.name
					if name == "nested named fns" && methods[si][i] {
						name = "method decls"
					}
					c[name] = append(c[name], censusHit{file: s.name, line: i + 1, text: ln})
				}
			}
		}
	}
	for _, s := range srcs {
		for i, ln := range s.lines {
			if !strings.Contains(ln, "+") {
				continue
			}
			for range incrementsByOne(ln) {
				c["increment by one"] = append(c["increment by one"], censusHit{file: s.name, line: i + 1, text: ln})
			}
		}
	}
	return c
}

func TestFeatureCensusMethodDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name    string
		source  string
		nested  int
		methods int
	}{
		{"nested visitor", "function outer(): void {\n  function visit(): void {}\n}\n", 1, 0},
		{"impl method", "impl mem.Drop for Root {\n  @noinline\n  function drop(self: Self): void {}\n}\n", 0, 1},
		{"nested in impl method", "impl T for S {\n  function run(self: Self): void {\n    function visit(): void {}\n  }\n}\n", 1, 1},
		{"trait methods", "trait T {\n  function declared(self: Self): void;\n  function defaulted(self: Self): void {\n    function visit(): void {}\n  }\n}\n", 1, 2},
		{"multiline impl", "impl T\nfor S\n{\n  function run(\n    self: Self\n  ): void {}\n}\nfunction outer(): void {\n  function visit(): void {}\n}\n", 1, 1},
		{"literal braces", "impl T for S {\n  function run(self: Self): void {\n    print(\"} impl X {\");\n    function visit(): void {}\n  }\n}\n", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := strippedSource{name: "probe.fern", lines: strings.Split(stripFernLiterals(tc.source), "\n")}
			c := takeCensus(t, []strippedSource{s})
			if got := c.n("nested named fns"); got != tc.nested {
				t.Errorf("nested named functions: got %d, want %d", got, tc.nested)
			}
			if got := c.n("method decls"); got != tc.methods {
				t.Errorf("method declarations: got %d, want %d", got, tc.methods)
			}
		})
	}
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

func pinned(t *testing.T, c census, name string, want int, why string) {
	t.Helper()
	if got := c.n(name); got != want {
		t.Errorf("%s: census counts %d, pinned at %d.\n"+
			"    %s\n"+
			"    Re-measure with `go test ./internal/testing/e2ecompiler/ -run TestSelfHostFeatureCensus -v`, then move BOTH this number and the row in docs/SELFHOST-LANGUAGE-FRICTION.md §1.\n"+
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
			"    Re-measure with `go test ./internal/testing/e2ecompiler/ -run TestSelfHostFeatureCensus -v`. Then either convert the new call sites, or — if the growth is legitimate — move the ceiling and say in the commit message what added the sites.\n"+
			"    sites:\n        %s", name, got, limit, measured, why, c.where(name))
	}
}

func TestSelfHostFeatureCensus(t *testing.T) {
	srcs := selfHostStripped(t)
	assertNoLiteralResidue(t, srcs)
	c := takeCensus(t, srcs)
	logCensus(t, srcs, c)

	pinned(t, c, "generic functions", 16,
		"Nine on astwalk's fold spine, five on its accumulator-carrying map spine (map_expr_where / map_exprs_where / map_stmt_where / map_stmts_where / map_stmts_where_changed), util.append_all, and util.sorted_by. The diagnostics source-order sort added sorted_by in #12993. This code exercises generic function monomorphisation on the fixpoint path.")
	pinned(t, c, "generic structs", 0,
		"The self-host declares no generic struct, so nothing on the fixpoint path monomorphises a generic TYPE — only generic functions. This is load-bearing, not incidental: a generic struct in a signature promotes its type param to the monomorphiser, and the per-module emit path runs no monomorphiser, so the un-cloned template fails IR verify there — the accumulator spine returns bare tuples for exactly that reason.")
	pinned(t, c, "arrow lambdas", 18,
		"Arrow lambdas in the compiler are traversal callbacks in astwalk, checker and parser, and the identity finish hook of the shared AST splicer. A new arrow lambda moves this row and the row in docs/SELFHOST-LANGUAGE-FRICTION.md.")
	pinned(t, c, "nested named fns", 89,
		"Nested named functions are visitors in astwalk, checker, parser, lift, wasm_ir, cover, treeshake and interp. A visitor that moves onto the shared folds lowers this row. A new visitor raises it.")
	pinned(t, c, "try op", 0,
		"The self-host propagates errors by hand, so `?` has NO fixpoint coverage. A rise here is good news and means this row and the doc's have to move.")
	pinned(t, c, "Map type spellings", 10,
		"The builtin Map is an association list, so each lookup is a linear scan. The compiler uses util.NameIndex for name lookups on hot paths. A new Map spelling needs a reason in the commit message, because a Map on a hot path makes that path quadratic.")

	atLeast(t, c, "for..in loops", 5790,
		"A floor on `for x in xs` loops. A conversion from a hand-written index loop raises the count: move the floor to the new measurement. A fold onto astwalk or a table index can remove loops: lower the floor and say which change it was in the commit message.")
	atLeast(t, c, "astwalk call sites", 516,
		"Hand-written AST walkers that move onto the shared fold spine raise this count. A fall means that a walker went back to its own traversal. Move the floor to the new measurement after each conversion.")

	atMost(t, c, "wildcard match arms", 1426, 1423,
		"A `_ =>` arm does not name its cases, so a new AST node can pass through it with no diagnostic. Some arms are necessary: a match over strings needs one (E030), and an arm that picks one shape out of a large sum is not safer when it names every variant. The ceiling is the measurement plus 3. Re-measure main when you move it, because a stale measurement gives the headroom to the next change.")
	atMost(t, c, "increment by one", 5063, 5060,
		"Each `x = x + 1` is usually a hand-written index loop that `for x in xs` can replace. The count is the size of that migration. The ceiling is the measurement plus 3.")
}

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

func TestFeatureCensusIncrementByOne(t *testing.T) {
	for _, tc := range []struct {
		line string
		want int
	}{
		{"i = i + 1;", 1},
		{"i += 1;", 1},
		{"i+=1;", 1},
		{"i = j + 1;", 0},
		{"i += 10;", 0},
		{"i += 1 + k;", 1},
		{"i = i + 12;", 0},
		{"i += 1; j = j + 1;", 2},
	} {
		if got := incrementsByOne(tc.line); got != tc.want {
			t.Errorf("incrementsByOne(%q) = %d, want %d", tc.line, got, tc.want)
		}
	}
}

func TestStripFernLiterals(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"plain code", `let i: i32 = 0;`, `let i: i32 = 0;`},
		{"line comment", `let i: i32 = 0; // count _ => arms`, `let i: i32 = 0; `},
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
		{"identifier ending in f", `let buf: string = "x";`, `let buf: string = "";`},
		{"lone apostrophe", `a ' b _ => c`, `a ' b _ => c`},
		{"unterminated string", "emit(\"oops\nlet i: i32 = 0;", "emit(\"\"\nlet i: i32 = 0;"},
		{"unterminated comment-free f-string", "out = f\"{a\nlet i: i32 = 0;", "out = \"\"\nlet i: i32 = 0;"},
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
