package e2eharness

import (
	"path/filepath"
	"sync"

	"github.com/jakechampion/lang/internal/testing/corpus"
)

// RepoPath joins elem onto the checkout root. The harness runs inside test
// packages at different depths, so a path relative to the working directory
// would name a different file for each caller.
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
