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

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const qualifierRationale = `a diagnostic must name a spelling that compiles; ` +
	`a bare trait name resolves to nothing without a prelude (E021 "unknown trait"). ` +
	`Write it qualified, e.g. ` + "`@derive(cmp.Eq)` / `impl cmp.Eq for T`" + `, ` +
	`and name the import that brings it into scope.`

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

	fernFiles, err := filepath.Glob(filepath.Join(root, "compiler", "*.fern"))
	inDrivers, _ := filepath.Glob(filepath.Join(root, "compiler", "drivers", "*.fern"))
	fernFiles = append(fernFiles, inDrivers...)
	if err != nil {
		t.Fatalf("glob compiler: %v", err)
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

	for _, want := range []string{
		filepath.Join("internal", "check", "checker", "checker.go"),
		filepath.Join("internal", "syntax", "parser", "parser.go"),
		filepath.Join("compiler", "checker.fern"),
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

func mustRel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

var diagCodeRe = regexp.MustCompile(`"[^"\n]*\b[EP][0-9]{3}\b`)

func emitsDiagnostics(src string) bool {
	return diagCodeRe.MatchString(src) ||
		strings.Contains(src, "Diag {") ||
		strings.Contains(src, "Code(")
}

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

type fernLiteral struct {
	text string
	line int
}

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

func TestUnqualifiedTraitNamesDiscriminates(t *testing.T) {
	bad := map[string][]string{
		"map key type %s is not supported — a struct used as a key must derive Eq and Hash (`@derive(Eq, Hash)`)":    {"Eq", "Hash"},
		"type does not implement `Eq` — add `@derive(Eq)` (or `impl Eq for %s`) so `==` can use structural equality": {"Eq", "Eq"},
		"type does not implement `Ord` — add `@derive(Ord)` (or `impl Ord for %s`)":                                  {"Ord", "Ord"},
		"does not implement `Display` — add `@derive(Display)`":                                                      {"Display"},
		"add `@derive(cmp.Eq, Hash)`": {"Hash"},
	}
	for msg, want := range bad {
		if got := unqualifiedTraitNames(msg); !slices.Equal(got, want) {
			t.Errorf("unqualifiedTraitNames(%q) = %v, want %v", msg, got, want)
		}
	}

	ok := []string{
		"map key type %s is not supported — a struct used as a key must derive Eq and Hash: add `@derive(cmp.Eq, cmp.Hash)`, which requires `import \"core/cmp\";`",
		"type does not implement `Eq` — add `@derive(cmp.Eq)` (or `impl cmp.Eq for %s`), which requires `import \"core/cmp\";`",
		"cannot @derive(%s) for %s: %s of type %s does not implement %s — add `impl %s for %s` (or remove the derive)",
		"`impl … for %s`: type must be a struct, enum, or built-in type",
		"cannot @derive(",
		"add `impl ",
		"cannot @derive(%s): only Eq, Display, Debug, Ord, Hash, Json, and Default are derivable",
		"implement Default by hand",
	}
	for _, msg := range ok {
		if got := unqualifiedTraitNames(msg); len(got) != 0 {
			t.Errorf("unqualifiedTraitNames(%q) = %v, want none", msg, got)
		}
	}
}

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
	fixture := "// the E053 no-allocation walk\n" +
		"let src: string = \"@derive(Eq)\\nstruct Pt { x: i32 }\";\n"
	if emitsDiagnostics(fixture) {
		t.Error("emitsDiagnostics matched a fixture across lines — the scan would judge formatter input as advice")
	}
}

var deriveHintCallRe = regexp.MustCompile(`\b(?:deriveHint|derive_hint)\(([^)]*)\)`)

var quotedArgRe = regexp.MustCompile(`"([^"]*)"`)

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
