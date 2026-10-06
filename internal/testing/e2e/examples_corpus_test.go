package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/corpus"
)

// examplesCorpus lists the runnable programs, repo-relative and
// slash-separated, in a stable order. The compiler's own sources are not in
// it: they need argv and a stdlib root to do anything, take minutes each to
// build, and are gated by internal/testing/e2ecompiler and the fixpoints. Programs
// without a main are in too; they build into something runnable anyway.
func examplesCorpus(t *testing.T) []string {
	t.Helper()
	root, err := corpus.RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	files, err := corpus.Files(root, corpus.Programs)
	if err != nil {
		t.Fatal(err)
	}
	return files
}
