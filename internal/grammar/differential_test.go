// The gate that keeps spec/grammar.ebnf honest.
//
// A grammar nobody checks is fiction within a month, and Fern had no
// grammar at all — the only description of its syntax was 5.9k lines of
// hand-written recursive descent. So the grammar ships with a
// differential test rather than as prose: every .fern source in the
// repository that internal/parser accepts, the grammar must also derive.
//
// The check is deliberately ONE-directional. The grammar is a superset:
// it derives `1 = 2`, which the parser rejects as P003, because
// assignability is a static rule rather than a syntactic one. Requiring
// the converse would mean encoding the static semantics into the
// grammar, which is exactly the mixing that makes a spec unreadable.
// spec/README.md lists the places the superset is intentional.
package grammar

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/lexer"
	"github.com/jakechampion/lang/internal/parser"
)

const grammarPath = "../../spec/grammar.ebnf"

// There is deliberately no known-gaps list. The gate is at 731/731, and
// a deferral mechanism with nothing in it is a hedge against a problem
// that does not exist — if the grammar stops deriving something, the
// answer is to fix the grammar, which is the whole point of writing it
// down.

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

// TestGrammarDerivesEveryParsedSource is the gate. The corpus is every
// .fern in the repo: the conformance cases, the examples, the stdlib,
// and the self-host compiler's own sources — which are by far the most
// adversarial input available, being the largest Fern programs written.
func TestGrammarDerivesEveryParsedSource(t *testing.T) {
	g := loadGrammar(t)
	covered := map[string]bool{}

	var checked, derived, skipped int
	for _, path := range fernSources(t) {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		// Only sources the real parser accepts are in scope: a file that
		// does not parse says nothing about the grammar.
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

	// A rule no real program exercises is not a description of Fern, it
	// is a guess about Fern. Writing this grammar produced several —
	// `use x = e;`, `race { … }`, a bare `x => e` lambda — all invented
	// from a keyword list rather than read out of the parser, and all
	// invisible to the derivation gate because nothing uses them. In a
	// normative document that is the worst kind of error: it reads
	// exactly like the parts that are true.
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

// fernSources walks the repo for .fern files. Generated and vendored
// trees are excluded; everything a human wrote is in scope.
func fernSources(t *testing.T) []string {
	t.Helper()
	root := "../.."
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
	return filepath.ToSlash(strings.TrimPrefix(filepath.Clean(p), "../../"))
}
