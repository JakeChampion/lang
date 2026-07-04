// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/test_runner_test.go.
package e2eharness

import (
	"path/filepath"
	"testing"
)

// LangSrcAbs joins the project root with the given relative
// path. The project root is two directories up from this
// test file (internal/e2e/ → internal/ → repo root).
func LangSrcAbs(t *testing.T, rel string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("filepath.Abs(%q): %v", rel, err)
	}
	return abs
}
