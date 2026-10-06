package e2e

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// examplesCorpusMinSize is the floor on the corpus WALK. A walk that selects
// nothing passes with no sub-tests at all, so a moved corpus has to read as a
// failure rather than a clean run.
const examplesCorpusMinSize = 250

// examplesCorpusRoots are the repo-relative directories whose programs make up
// the corpus: the examples, the Fern-side tests and probes, and the benchmarks.
// The compiler's own sources are not in it: they need argv and a stdlib root to
// do anything, take minutes each to build, and are gated by
// internal/e2eselfhost and the fixpoints.
var examplesCorpusRoots = []string{"examples", "tests", "bench"}

// examplesCorpus lists the corpus, repo-relative and slash-separated, in a
// stable order. Programs without a main are in too; they build into something
// runnable anyway.
func examplesCorpus(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, sub := range examplesCorpusRoots {
		root := langSrcAbs(t, sub)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || filepath.Ext(path) != ".fern" {
				return nil
			}
			out = append(out, filepath.ToSlash(filepath.Join(sub, strings.TrimPrefix(path, root))))
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	sort.Strings(out)
	if len(out) < examplesCorpusMinSize {
		t.Fatalf("the corpus walk under %v found %d .fern programs, below the %d floor — a walk that "+
			"selects nothing passes with no sub-tests at all, so this is a moved corpus rather than "+
			"a clean run", examplesCorpusRoots, len(out), examplesCorpusMinSize)
	}
	return out
}
