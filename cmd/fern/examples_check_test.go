package main

import (
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/corpus"
)

// Every example, Fern-side test and benchmark checks clean. The Examples lane
// compiles only the top-level `examples/*.fern`, and the printer corpus only
// parses, so a program under a subdirectory could stop type-checking with
// nothing noticing — two probes did. The compiler's sources have gates of
// their own.
func TestEveryExampleChecks(t *testing.T) {
	root := repoRoot(t)
	files, err := corpus.Files(root, corpus.Programs)
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
