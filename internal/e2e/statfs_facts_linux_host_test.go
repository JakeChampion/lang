//go:build linux

package e2e

import (
	"syscall"
	"testing"
)

// hostFsFacts is what the builtin must agree with for `dir`: the block size
// the counts are in, the longest name the filesystem accepts, and the longest
// path the kernel resolves. Linux reports the first two from `statfs(2)` and
// has no pathconf syscall at all, so PATH_MAX is its own 4096 — what `getconf
// PATH_MAX` reports for every filesystem it mounts.
func hostFsFacts(t *testing.T, dir string) (blockSize, nameMax, pathMax int64) {
	t.Helper()
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		t.Fatalf("statfs %s: %v", dir, err)
	}
	return int64(st.Bsize), int64(st.Namelen), 4096
}
