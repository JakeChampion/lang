package e2e

import (
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
)

// examplesCorpusMinSize is the floor on the corpus WALK. A walk that selects
// nothing passes with no sub-tests at all, so a moved corpus has to read as a
// failure rather than a clean run.
const examplesCorpusMinSize = 250

// examplesCorpus lists the examples corpus, repo-relative and slash-separated,
// in a stable order.
//
// examples/self_host is excluded: those files are the self-host compiler's own
// sources, which need argv and a stdlib root to do anything, take minutes each
// to build, and are gated by internal/e2eselfhost and the fixpoints. Everything
// else under examples/ is in — including the programs that turn out not to have
// a main, which build into something runnable anyway.
func examplesCorpus(t *testing.T) []string {
	t.Helper()
	root := langSrcAbs(t, "examples")
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "self_host" {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".fern" {
			return nil
		}
		// Relative to the parent of examples/, so the key is the repo-relative
		// path the testdata files and langSrcAbs both speak.
		rel, relErr := filepath.Rel(filepath.Dir(root), path)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(out)
	if len(out) < examplesCorpusMinSize {
		t.Fatalf("the corpus walk under %s found %d .fern programs, below the %d floor — a walk that "+
			"selects nothing passes with no sub-tests at all, so this is a moved corpus rather than "+
			"a clean run", root, len(out), examplesCorpusMinSize)
	}
	return out
}
