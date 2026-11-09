package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/corpus"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestEveryExampleChecks(t *testing.T) {
	root := repoRoot(t)
	files, err := corpus.Files(root, append(append([]string{}, corpus.Programs...), "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	files, err = corpus.Shard(files, os.Getenv("FERN_CHECK_CORPUS_SHARD"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range files {
		path := filepath.Join(root, rel)
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			out, exit := e2eharness.InProcessVerdict(t, "cmd/fern runCheck", path, func() ([]byte, int) {
				if err := runCheck(path, ""); err != nil {
					return []byte(err.Error()), 1
				}
				return nil, 0
			})
			if exit != 0 {
				t.Errorf("%s", out)
			}
		})
	}
}
