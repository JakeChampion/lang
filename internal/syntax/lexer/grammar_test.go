package lexer

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// grammarKeywordScopes are the TextMate scopes the VS Code grammar gives the
// lexer's reserved words. Contextual names (`fip`, `async`, ...) are scoped
// otherwise and stay out of this comparison.
var grammarKeywordScopes = map[string]bool{
	"keyword.control.fern":        true,
	"storage.type.fern":           true,
	"constant.language.fern":      true,
	"support.type.primitive.fern": true,
}

var wordAlternation = regexp.MustCompile(`^\\b\(([a-z0-9]+(?:\|[a-z0-9]+)*)\)\\b$`)

// TestGrammarHighlightsExactlyTheKeywords holds the VS Code grammar's keyword
// set to the Go lexer's table and to compiler/lexer.fern's keywords().
func TestGrammarHighlightsExactlyTheKeywords(t *testing.T) {
	src, err := os.ReadFile("../../../editors/vscode/syntaxes/fern.tmLanguage.json")
	if err != nil {
		t.Fatal(err)
	}
	var grammar struct {
		Repository map[string]struct {
			Patterns []struct {
				Name  string `json:"name"`
				Match string `json:"match"`
			} `json:"patterns"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(src, &grammar); err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, rule := range grammar.Repository {
		for _, p := range rule.Patterns {
			if !grammarKeywordScopes[p.Name] {
				continue
			}
			m := wordAlternation.FindStringSubmatch(p.Match)
			if m == nil {
				t.Fatalf("%s: match %q is not a \\b(word|...)\\b alternation", p.Name, p.Match)
			}
			for _, w := range strings.Split(m[1], "|") {
				if prev, dup := seen[w]; dup {
					t.Errorf("%q is in both %s and %s", w, prev, p.Name)
				}
				seen[w] = p.Name
			}
		}
	}
	got := make([]string, 0, len(seen))
	for w := range seen {
		got = append(got, w)
	}
	sort.Strings(got)

	if want := Keywords(); !reflect.DeepEqual(got, want) {
		t.Errorf("grammar keywords:\n  %v\nwant the Go lexer's:\n  %v", got, want)
	}
	if want := fernLexerKeywords(t); !reflect.DeepEqual(got, want) {
		t.Errorf("grammar keywords:\n  %v\nwant compiler/lexer.fern's keywords():\n  %v", got, want)
	}
}

// TestFernLexerKeywordTableMatchesItsList holds compiler/lexer.fern's
// is_keyword, the byte-dispatched comparison chain the lexer tokenises with,
// to the keywords() list it is documented to mirror, which the compiler uses
// to reject a keyword as a name and the language server to complete names.
func TestFernLexerKeywordTableMatchesItsList(t *testing.T) {
	src, err := os.ReadFile("../../../compiler/lexer.fern")
	if err != nil {
		t.Fatal(err)
	}
	m := fernIsKeywordBody.FindSubmatch(src)
	if m == nil {
		t.Fatal("compiler/lexer.fern: no `function is_keyword(name: string): boolean { ... }`")
	}
	var got []string
	for _, lit := range fernNameCompare.FindAllSubmatch(m[1], -1) {
		got = append(got, string(lit[1]))
	}
	sort.Strings(got)
	if want := fernLexerKeywords(t); !reflect.DeepEqual(got, want) {
		t.Errorf("is_keyword compares against:\n  %v\nwant keywords():\n  %v", got, want)
	}
}

var (
	fernKeywordsBody  = regexp.MustCompile(`pub function keywords\(\): string\[\] \{\s*return \[([^\]]*)\];`)
	fernIsKeywordBody = regexp.MustCompile(`(?s)\nfunction is_keyword\(name: string\): boolean \{\n(.*?)\n\}\n`)
	fernNameCompare   = regexp.MustCompile(`name == "([a-z0-9]+)"`)
)

func fernLexerKeywords(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("../../../compiler/lexer.fern")
	if err != nil {
		t.Fatal(err)
	}
	m := fernKeywordsBody.FindSubmatch(src)
	if m == nil {
		t.Fatal("compiler/lexer.fern: no `pub function keywords(): string[] { return [...]; }`")
	}
	var out []string
	for _, q := range strings.Split(string(m[1]), ",") {
		out = append(out, strings.Trim(strings.TrimSpace(q), `"`))
	}
	sort.Strings(out)
	return out
}
