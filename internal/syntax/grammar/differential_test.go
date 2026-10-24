package grammar

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/lexer"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

const grammarPath = "../../../spec/grammar.ebnf"

func loadGrammar(t *testing.T) *Grammar {
	t.Helper()
	src, err := os.ReadFile(grammarPath)
	if err != nil {
		t.Fatalf("read grammar: %v", err)
	}
	g, err := Parse(string(src))
	if err != nil {
		t.Fatalf("parse grammar: %v", err)
	}
	return g
}

func TestGrammarIsWellFormed(t *testing.T) {
	g := loadGrammar(t)
	if bad := g.LeftRecursive(); len(bad) > 0 {
		t.Errorf("left-recursive rules never match: %s", strings.Join(bad, ", "))
	}
	if dead := g.Unreachable(); len(dead) > 0 {
		t.Errorf("unreachable rules — they read as normative but describe nothing: %s", strings.Join(dead, ", "))
	}
}

func TestGrammarDerivesEveryParsedSource(t *testing.T) {
	g := loadGrammar(t)
	covered := map[string]bool{}

	var checked, derived, skipped int
	for _, path := range fernSources(t) {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if _, perr := parser.Parse(string(src)); perr != nil {
			skipped++
			continue
		}
		checked++

		toks, _, lerr := lexer.Tokenize(string(src))
		if lerr != nil {
			t.Errorf("%s: parser accepted it but the lexer did not: %v", path, lerr)
			continue
		}

		if ok, stuck, used := g.MatchCoverage(toks); ok {
			derived++
			for _, r := range used {
				covered[r] = true
			}
		} else {
			t.Errorf("%s: grammar cannot derive this source, stuck at:\n    %s",
				relPath(path), Context(toks, stuck))
		}
	}

	if checked == 0 {
		t.Fatalf("no parseable .fern sources found — the gate is not running")
	}
	t.Logf("grammar derived %d/%d parseable sources (%d unparseable, skipped)", derived, checked, skipped)

	var unexercised []string
	for _, name := range g.RuleNames() {
		if !covered[name] {
			unexercised = append(unexercised, name)
		}
	}
	if len(unexercised) > 0 {
		t.Errorf("no source in the repository exercises these rules, so nothing shows whether they describe Fern:\n    %s",
			strings.Join(unexercised, ", "))
	}
}

func fernSources(t *testing.T) []string {
	t.Helper()
	root := "../../.."
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "target", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".fern") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(out)
	return out
}

func relPath(p string) string {
	return filepath.ToSlash(strings.TrimPrefix(filepath.Clean(p), "../../../"))
}
