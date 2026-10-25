package main

import (
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/corpus"
)

func TestEveryExampleChecks(t *testing.T) {
	root := repoRoot(t)
	files, err := corpus.Files(root, append(append([]string{}, corpus.Programs...), "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range files {
		path := filepath.Join(root, rel)
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			if err := runCheck(path, ""); err != nil {
				t.Errorf("%v", err)
			}
		})
	}
}
