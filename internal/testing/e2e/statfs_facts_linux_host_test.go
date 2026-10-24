//go:build linux

package e2e

import (
	"syscall"
	"testing"
)

func hostFsFacts(t *testing.T, dir string) (blockSize, nameMax, pathMax int64) {
	t.Helper()
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		t.Fatalf("statfs %s: %v", dir, err)
	}
	return int64(st.Bsize), int64(st.Namelen), 4096
}

func hostFsIdentity(t *testing.T, dir string) (fsType, fsid, fragSize int64) {
	t.Helper()
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		t.Fatalf("statfs %s: %v", dir, err)
	}
	v := st.Fsid.X__val
	return st.Type, int64(uint64(uint32(v[0]))<<32 | uint64(uint32(v[1]))), st.Frsize
}
