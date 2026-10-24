//go:build darwin

package e2e

import (
	"syscall"
	"testing"
)

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

func hostFsIdentity(t *testing.T, dir string) (fsType, fsid, fragSize int64) {
	t.Helper()
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		t.Fatalf("statfs %s: %v", dir, err)
	}
	v := st.Fsid.Val
	return int64(st.Type), int64(uint64(uint32(v[0]))<<32 | uint64(uint32(v[1]))), int64(st.Bsize)
}
