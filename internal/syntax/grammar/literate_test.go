package grammar

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestGrammarIncludesLiteratePrograms(t *testing.T) {
	root := t.TempDir()
	documents := map[string]string{
		"module.fern.md": "```fern\n<<*>>=\n<<body>>\n```\n\n```fern\n<<body>>=\npub const VALUE: i32 = 1;\n```\n",
		"multi.fern.md":  "```fern file=a.fern\nfunction a(): i32 { return 1; }\n```\n\n```fern file=b.fern\nfunction b(): i32 { return 2; }\n```\n",
		"prose.fern.md":  "```fern\nthis is a display example, not a module\n```\n",
	}
	for name, document := range documents {
		if err := os.WriteFile(filepath.Join(root, name), []byte(document), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	g := loadGrammar(t)
	counts := map[string]int{}
	for _, path := range fernSources(t, root) {
		sources, err := grammarSources(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		counts[filepath.Base(path)] = len(sources)
		for _, source := range sources {
			if _, err := parser.Parse(source.Code); err != nil {
				t.Fatalf("%s [%s]: parser: %v", path, source.Path, err)
			}
			if ok, stuck := derives(t, g, source.Code); !ok {
				t.Errorf("%s [%s]: grammar stopped at %s", path, source.Path, stuck)
			}
		}
	}
	for name, want := range map[string]int{"module.fern.md": 1, "multi.fern.md": 2, "prose.fern.md": 0} {
		if got, found := counts[name]; !found || got != want {
			t.Errorf("%s: found=%v, modules=%d, want %d", name, found, got, want)
		}
	}
}

func TestGrammarReportsInvalidLiterateProgram(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.fern.md")
	if err := os.WriteFile(path, []byte("```fern\n<<*>>=\n<<undefined>>\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := grammarSources(path); err == nil || !strings.Contains(err.Error(), "undefined chunk") {
		t.Fatalf("invalid literate program: %v", err)
	}
}
