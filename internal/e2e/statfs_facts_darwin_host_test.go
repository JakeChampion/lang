//go:build darwin

package e2e

import (
	"syscall"
	"testing"
)

// hostFsFacts on Darwin takes three calls, as the builtin itself does: its
// `struct statfs` has the block size and none of the length limits, so both
// come from `pathconf(2)`, which Darwin has and Linux does not.
func hostFsFacts(t *testing.T, dir string) (blockSize, nameMax, pathMax int64) {
	t.Helper()
	const (
		pcNameMax = 4
		pcPathMax = 5
	)
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		t.Fatalf("statfs %s: %v", dir, err)
	}
	name, err := syscall.Pathconf(dir, pcNameMax)
	if err != nil {
		t.Fatalf("pathconf _PC_NAME_MAX %s: %v", dir, err)
	}
	path, err := syscall.Pathconf(dir, pcPathMax)
	if err != nil {
		t.Fatalf("pathconf _PC_PATH_MAX %s: %v", dir, err)
	}
	return int64(st.Bsize), int64(name), int64(path)
}
