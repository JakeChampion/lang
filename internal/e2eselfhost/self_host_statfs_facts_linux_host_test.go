//go:build linux

package e2eselfhost

import (
	"syscall"
	"testing"
)

// selfHostFsFacts reads what the self-host's `statfs` must agree with for
// `dir`, through the same `statfs(2)` the emitted helper issues. Linux reports
// the whole record including `f_namelen`, and has no pathconf syscall at all,
// so PATH_MAX is the kernel's own 4096 — what `getconf PATH_MAX` reports for
// every filesystem it mounts.
func selfHostFsFacts(t *testing.T, dir string) hostFs {
	t.Helper()
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		t.Fatalf("statfs %s: %v", dir, err)
	}
	return hostFs{
		blockSize:   int64(st.Bsize),
		blocks:      int64(st.Blocks),
		blocksFree:  int64(st.Bfree),
		blocksAvail: int64(st.Bavail),
		files:       int64(st.Files),
		filesFree:   int64(st.Ffree),
		nameMax:     int64(st.Namelen),
		pathMax:     4096,
	}
}
