//go:build linux

package e2ecompiler

import (
	"syscall"
	"testing"
)

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
		fsType:      st.Type,
		fsid:        int64(uint64(uint32(st.Fsid.X__val[0]))<<32 | uint64(uint32(st.Fsid.X__val[1]))),
		fragSize:    st.Frsize,
	}
}
