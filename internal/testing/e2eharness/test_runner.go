// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/testing/e2e and
// internal/testing/e2ecompiler (#4398 part 3). Extracted verbatim from
// internal/testing/e2e/test_runner_test.go.
package e2eharness

import "testing"

// LangSrcAbs is the absolute path of rel, a path from the checkout root.
func LangSrcAbs(t *testing.T, rel string) string {
	t.Helper()
	return RepoPath(rel)
}
