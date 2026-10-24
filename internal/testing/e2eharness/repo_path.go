package e2eharness

import (
	"path/filepath"
	"sync"

	"github.com/jakechampion/lang/internal/testing/corpus"
)

func RepoPath(elem ...string) string {
	return filepath.Join(append([]string{repoRoot()}, elem...)...)
}

var repoRoot = sync.OnceValue(func() string {
	root, err := corpus.RepoRoot()
	if err != nil {
		panic(err)
	}
	return root
})
