// Package sourcelint holds fast, dependency-free repo-hygiene checks that run
// in the ordinary `go test ./...` lane (no build tools, no fixtures).
package sourcelint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// identRe matches a plain trait identifier — the only shape this lint
// judges. Everything else in a suggestion slot is a placeholder.
var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// There is no prelude, and `@derive` / `impl` resolve a trait by the name
// as written, so a diagnostic that spells a bare `@derive(Eq)` sends the
// reader straight into `error[E021]: @derive(Eq): unknown trait`. Four
// messages did exactly that for a long time and nothing noticed (#6990):
// the checker differential compares CODE SETS between the two compilers
// and never their message text, so both were free to say the same wrong
// thing (#7018).
//
// The paired tests in internal/checker cover the sites they name. This
// one covers every site there is, in BOTH compilers, without running
// either: a trait name written LITERALLY inside a diagnostic's
// `@derive(…)` or `impl … for …` must be module-qualified. That makes
// the failure shape unrepresentable rather than merely tested — a new
// hint cannot be added wrong, in Go or in Fern.
//
// A name that arrives by substitution is exempt, and deliberately so.
// checker.go's "add `impl %s for %s`" and checker.fern's concatenated
// equivalent both echo the trait the READER wrote, which already
// resolved — E021 only fires past that point — so echoing it bare is
// correct, and demanding a qualifier there would be the wrong lesson.
const qualifierRationale = `a diagnostic must name a spelling that compiles; ` +
	`a bare trait name resolves to nothing without a prelude (E021 "unknown trait"). ` +
	`Write it qualified, e.g. ` + "`@derive(cmp.Eq)` / `impl cmp.Eq for T`" + `, ` +
	`and name the import that brings it into scope.`

// suggestionFinding is one offending literal.
type suggestionFinding struct {
	file string
	line int
	lit  string
	name string
}

func TestDiagnosticSuggestionsNameAQualifiedTrait(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}

	var findings []suggestionFinding
	var goProducers, fernProducers []string

	// The native compiler: every non-test Go file that can print a
	// diagnostic. Parsing rather than grepping is what keeps the check
	// honest — `@derive(Trait, …)` shows up in a dozen doc comments, and
	// only a STRING can reach a user.
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			if !emitsDiagnostics(string(b)) {
				return nil
			}
			goProducers = append(goProducers, mustRel(root, path))
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Fatalf("parse %s: %v", path, perr)
			}
			rel := mustRel(root, path)
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, uerr := strconv.Unquote(lit.Value)
				if uerr != nil {
					return true
				}
				for _, name := range unqualifiedTraitNames(s) {
					findings = append(findings, suggestionFinding{rel, fset.Position(lit.Pos()).Line, s, name})
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	// The self-hosted compiler. Its diagnostics are plain string literals
	// spliced with `+`, so a hand scanner that knows comments from strings
	// is the whole parser this needs.
	fernFiles, err := filepath.Glob(filepath.Join(root, "examples", "self_host", "*.fern"))
	if err != nil {
		t.Fatalf("glob self_host: %v", err)
	}
	for _, path := range fernFiles {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !emitsDiagnostics(string(b)) {
			continue
		}
		rel := mustRel(root, path)
		fernProducers = append(fernProducers, rel)
		for _, sl := range fernStringLiterals(string(b)) {
			for _, name := range unqualifiedTraitNames(sl.text) {
				findings = append(findings, suggestionFinding{rel, sl.line, sl.text, name})
			}
		}
	}

	// The same rule, one level up. Both compilers now build these hints
	// through a shared helper, which moves the trait name out of the
	// message literal and out of reach of the scan above — a bare
	// `deriveHint(tn, "Eq", "Eq")` would print `@derive(Eq)` and pass.
	// The helper's own arguments are therefore held to the same standard.
	for _, path := range append(goProducers, fernProducers...) {
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for line, args := range deriveHintCallArgs(string(b)) {
			for _, name := range args {
				findings = append(findings, suggestionFinding{path, line, "deriveHint(… " + name + " …)", name})
			}
		}
	}

	// A green run has to mean "every producer was read", not "the walk
	// found nothing to read". Both compilers' main diagnostic sites are
	// named so a refactor that moves them somewhere emitsDiagnostics no
	// longer recognises fails here instead of silently emptying the scan.
	for _, want := range []string{
		filepath.Join("internal", "checker", "checker.go"),
		filepath.Join("internal", "parser", "parser.go"),
		filepath.Join("examples", "self_host", "checker.fern"),
	} {
		if !slices.Contains(append(goProducers, fernProducers...), want) {
			t.Errorf("%s was not scanned — emitsDiagnostics no longer recognises it, so this lint is looking at less than it claims", want)
		}
	}
	for _, f := range findings {
		t.Errorf("%s:%d: diagnostic names the unqualified trait %q\n  in: %s\n  %s",
			f.file, f.line, f.name, f.lit, qualifierRationale)
	}
}

// mustRel is filepath.Rel with the error folded into the result, for
// paths already known to sit under root.
func mustRel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

// diagCodeRe matches a diagnostic code inside a string. A file that
// carries one, or that builds the self-host's `Diag`, is a file whose
// strings can reach a user.
var diagCodeRe = regexp.MustCompile(`"[^"\n]*\b[EP][0-9]{3}\b`)

// emitsDiagnostics reports whether src can print a diagnostic, and so
// whether its string literals are user-facing copy. It is what keeps a
// program FIXTURE out of the scan: examples/self_host/printer.fern's
// round-trip corpus is full of `@derive(Eq)` written as input to the
// formatter, which never reaches a checker and is not advice to anyone.
func emitsDiagnostics(src string) bool {
	return diagCodeRe.MatchString(src) ||
		strings.Contains(src, "Diag {") ||
		strings.Contains(src, "Code(")
}

// unqualifiedTraitNames returns the bare (unqualified) trait names a
// message spells inside `@derive(…)` or `impl … for …`. A name is exempt
// unless it is a plain identifier: one carrying a `.` is already
// qualified, `%s` is a format verb filled at runtime, `…` is prose
// standing in for "any trait", and an empty one is where a literal ends
// and a concatenation takes over.
func unqualifiedTraitNames(s string) []string {
	var out []string
	bare := func(name string) bool { return identRe.MatchString(name) }

	for rest := s; ; {
		i := strings.Index(rest, "@derive(")
		if i < 0 {
			break
		}
		rest = rest[i+len("@derive("):]
		end := strings.IndexByte(rest, ')')
		if end < 0 {
			// The literal ends inside the attribute: the argument is
			// spliced in, which is the exempt case.
			break
		}
		for _, name := range strings.Split(rest[:end], ",") {
			if name = strings.TrimSpace(name); bare(name) {
				out = append(out, name)
			}
		}
		rest = rest[end:]
	}

	for rest := s; ; {
		i := strings.Index(rest, "impl ")
		if i < 0 {
			break
		}
		rest = rest[i+len("impl "):]
		// Only an `impl X for` is a spelling to write; `impl Trait` alone
		// (or a sentence that happens to start "impl ") is not.
		name, after, ok := strings.Cut(rest, " ")
		if !ok || !strings.HasPrefix(after, "for ") {
			continue
		}
		if bare(strings.Trim(name, "`")) {
			out = append(out, name)
		}
	}
	return out
}

// fernLiteral is one double-quoted string from a .fern source.
type fernLiteral struct {
	text string
	line int
}

// fernStringLiterals extracts the double-quoted literals from src,
// skipping `//` and `/* */` comments so a quote inside prose is not read
// as the start of a string. Escapes are unfolded only far enough to keep
// `\"` from ending a literal early — the scanner reads spellings, not
// values.
func fernStringLiterals(src string) []fernLiteral {
	var out []fernLiteral
	line := 1
	for i := 0; i < len(src); {
		switch {
		case src[i] == '\n':
			line++
			i++
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case strings.HasPrefix(src[i:], "/*"):
			for i += 2; i < len(src) && !strings.HasPrefix(src[i:], "*/"); i++ {
				if src[i] == '\n' {
					line++
				}
			}
			i += 2
		case src[i] == '"':
			startLine := line
			var b strings.Builder
			for i++; i < len(src) && src[i] != '"'; i++ {
				if src[i] == '\\' && i+1 < len(src) {
					if src[i+1] == '"' {
						b.WriteByte('"')
					} else {
						b.WriteByte(src[i])
						b.WriteByte(src[i+1])
					}
					i++
					continue
				}
				if src[i] == '\n' {
					line++
				}
				b.WriteByte(src[i])
			}
			i++
			out = append(out, fernLiteral{b.String(), startLine})
		default:
			i++
		}
	}
	return out
}

// A clean tree is the state this lint reports for both "nothing is wrong"
// and "the matcher stopped matching". These cases separate the two: every
// `bad` row is a real hint that shipped or could ship, every `ok` row is a
// shape the lint has to stay quiet about, and the exempt ones are the
// reason it cannot simply grep for `@derive(`.
func TestUnqualifiedTraitNamesDiscriminates(t *testing.T) {
	bad := map[string][]string{
		// The four #6990 messages, as they read before #7000.
		"map key type %s is not supported — a struct used as a key must derive Eq and Hash (`@derive(Eq, Hash)`)":    {"Eq", "Hash"},
		"type does not implement `Eq` — add `@derive(Eq)` (or `impl Eq for %s`) so `==` can use structural equality": {"Eq", "Eq"},
		"type does not implement `Ord` — add `@derive(Ord)` (or `impl Ord for %s`)":                                  {"Ord", "Ord"},
		"does not implement `Display` — add `@derive(Display)`":                                                      {"Display"},
		// Half-qualified: one name fixed, its sibling missed.
		"add `@derive(cmp.Eq, Hash)`": {"Hash"},
	}
	for msg, want := range bad {
		if got := unqualifiedTraitNames(msg); !slices.Equal(got, want) {
			t.Errorf("unqualifiedTraitNames(%q) = %v, want %v", msg, got, want)
		}
	}

	ok := []string{
		// What the four now say.
		"map key type %s is not supported — a struct used as a key must derive Eq and Hash: add `@derive(cmp.Eq, cmp.Hash)`, which requires `import \"core/cmp\";`",
		"type does not implement `Eq` — add `@derive(cmp.Eq)` (or `impl cmp.Eq for %s`), which requires `import \"core/cmp\";`",
		// Echoes of the reader's own (already resolved) spelling.
		"cannot @derive(%s) for %s: %s of type %s does not implement %s — add `impl %s for %s` (or remove the derive)",
		"`impl … for %s`: type must be a struct, enum, or built-in type",
		// A literal that ends where a concatenation takes over, which is
		// how every self-host message is built.
		"cannot @derive(",
		"add `impl ",
		// Prose that merely names the traits, with no spelling to copy.
		"cannot @derive(%s): only Eq, Display, Debug, Ord, Hash, Json, and Default are derivable",
		"implement Default by hand",
	}
	for _, msg := range ok {
		if got := unqualifiedTraitNames(msg); len(got) != 0 {
			t.Errorf("unqualifiedTraitNames(%q) = %v, want none", msg, got)
		}
	}
}

// The hint builders take the trait name as an argument, so the rule has
// to reach their call sites too — that is where a bare name hides once a
// message stops spelling it inline.
func TestDeriveHintCallArgsJudgesTheSpelling(t *testing.T) {
	src := `c.errfCode(n.P, "E041", "...%s...", deriveHint(tn, "Eq", "Eq"))
out.append(dg_at("E041", "..." + derive_hint(recv, "cmp.Eq, Hash", "") + "...", l, c));
c.errfCode(n.P, "E045", "...%s...", deriveHint(kt.Name, "cmp.Eq, cmp.Hash", ""))
` + "someOtherCall(x, \"Eq\")\n"
	got := deriveHintCallArgs(src)
	want := map[int][]string{1: {"Eq", "Eq"}, 2: {"Hash"}}
	if len(got) != len(want) {
		t.Fatalf("deriveHintCallArgs = %v, want %v", got, want)
	}
	for line, names := range want {
		if !slices.Equal(got[line], names) {
			t.Errorf("line %d = %v, want %v", line, got[line], names)
		}
	}
}

// The Fern scanner has to tell a string from the prose around it, or the
// self-host half of the lint reads comments and misses messages.
func TestFernStringLiteralsSkipsComments(t *testing.T) {
	src := `// a comment with a "quote and @derive(Eq) in it
function f(): string {
  /* @derive(Ord) in a block comment
     spanning lines */
  return "add ` + "`@derive(cmp.Eq)`" + `" + tn + " here \"quoted\"";
}
`
	var got []string
	for _, sl := range fernStringLiterals(src) {
		got = append(got, sl.text)
	}
	want := []string{"add `@derive(cmp.Eq)`", " here \"quoted\""}
	if !slices.Equal(got, want) {
		t.Fatalf("fernStringLiterals = %q, want %q", got, want)
	}
}

// emitsDiagnostics picks the files whose strings are copy. Getting it
// wrong in the permissive direction pulls in the formatter's round-trip
// fixtures; getting it wrong in the strict direction empties the scan.
func TestEmitsDiagnosticsSeparatesCopyFromFixtures(t *testing.T) {
	for _, src := range []string{
		`c.errfCode(n.P, "E041", "cannot compare")`,
		`out = out.append(dg_at("E021", "unknown trait", l, c));`,
		`return checker.Diag { code: code, message: m, line: 0, col: 0 };`,
	} {
		if !emitsDiagnostics(src) {
			t.Errorf("emitsDiagnostics(%q) = false, want true", src)
		}
	}
	// A formatter fixture: a program in a string, and a comment
	// mentioning a code on another line. The two must not combine into a
	// match — that is what a `[^"]*` spanning newlines did.
	fixture := "// the E053 no-allocation walk\n" +
		"var src: string = \"@derive(Eq)\\nstruct Pt { x: i32 }\";\n"
	if emitsDiagnostics(fixture) {
		t.Error("emitsDiagnostics matched a fixture across lines — the scan would judge formatter input as advice")
	}
}

// deriveHintCallRe matches a call to either compiler's shared hint
// builder — checker.go's deriveHint, checker.fern's derive_hint. Their
// calls carry no nested parentheses, so a flat argument slice is enough.
var deriveHintCallRe = regexp.MustCompile(`\b(?:deriveHint|derive_hint)\(([^)]*)\)`)

// quotedArgRe pulls the double-quoted arguments out of such a call. The
// receiver argument is an identifier and is skipped by construction.
var quotedArgRe = regexp.MustCompile(`"([^"]*)"`)

// deriveHintCallArgs returns, per source line, the unqualified trait names
// passed to a hint builder. A `@derive` argument may name several traits
// ("cmp.Eq, cmp.Hash"), so each is judged on its own.
func deriveHintCallArgs(src string) map[int][]string {
	out := map[int][]string{}
	for i, line := range strings.Split(src, "\n") {
		for _, call := range deriveHintCallRe.FindAllStringSubmatch(line, -1) {
			for _, arg := range quotedArgRe.FindAllStringSubmatch(call[1], -1) {
				for _, name := range strings.Split(arg[1], ",") {
					if name = strings.TrimSpace(name); identRe.MatchString(name) {
						out[i+1] = append(out[i+1], name)
					}
				}
			}
		}
	}
	return out
}
