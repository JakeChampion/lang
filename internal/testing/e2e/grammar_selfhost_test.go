package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/grammar"
	"github.com/jakechampion/lang/internal/syntax/lexer"
)

// TestGrammarDerivesSelfHostParsedSources is internal/syntax/grammar's
// differential with the parser syntax lands in first: every .fern source in
// the repository that the self-host parser accepts, spec/grammar.ebnf must
// derive. `fern -fmt` is the probe, since it parses a file on its own and
// refuses one that does not parse.
func TestGrammarDerivesSelfHostParsedSources(t *testing.T) {
	requireSelfHostFixtureLeg(t)
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host CLI driver runs only natively (argv paths)")
	}
	src, err := os.ReadFile("../../../spec/grammar.ebnf")
	if err != nil {
		t.Fatalf("read grammar: %v", err)
	}
	g, err := grammar.Parse(string(src))
	if err != nil {
		t.Fatalf("parse grammar: %v", err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	var checked int
	for _, path := range repoFernSources(t) {
		if exec.Command(fernBin, "-fmt", path).Run() != nil {
			continue
		}
		checked++
		text, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		toks, _, lerr := lexer.Tokenize(string(text))
		if lerr != nil {
			t.Errorf("%s: the self-host parser accepts it but the Go lexer does not: %v", path, lerr)
			continue
		}
		if ok, stuck := g.Match(toks); !ok {
			t.Errorf("%s: the self-host parser accepts it but the grammar cannot derive it, stuck at:\n    %s", path, grammar.Context(toks, stuck))
		}
	}
	if checked == 0 {
		t.Fatalf("the self-host parser accepted no .fern source — the gate is not running")
	}
	t.Logf("grammar derived every one of the %d sources the self-host parser accepts", checked)
}

// repoFernSources is every .fern file in the repository but generated and
// vendored trees.
func repoFernSources(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir("../../..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "target", "bin", "build":
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
