package e2eharness

import "testing"

func LangSrcAbs(t *testing.T, rel string) string {
	t.Helper()
	return RepoPath(rel)
}
