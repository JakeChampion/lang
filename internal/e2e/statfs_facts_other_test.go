//go:build !linux && !darwin

package e2e

import (
	"runtime"
	"testing"
)

// hostFsFacts has nothing to compare against on a platform this package has
// no `statfs(2)` projection for — the same platforms `internal/interp`'s
// fsstat_other.go answers ENOSYS on, so there is no answer to disagree with
// rather than an answer nobody checked.
func hostFsFacts(t *testing.T, _ string) (blockSize, nameMax, pathMax int64) {
	t.Helper()
	t.Skipf("the statfs probe compares against statfs(2); %s has none here", runtime.GOOS)
	return 0, 0, 0
}
