package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/corpus"
)

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
