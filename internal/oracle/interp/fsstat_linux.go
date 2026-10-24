//go:build linux

package interp

import "syscall"

func fsStatFields(path string) (rawFsStat, error) {
	const linuxPathMax = 4096
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return rawFsStat{}, err
	}
	return rawFsStat{
		blockSize:   int64(st.Bsize),
		blocks:      int64(st.Blocks),
		blocksFree:  int64(st.Bfree),
		blocksAvail: int64(st.Bavail),
		files:       int64(st.Files),
		filesFree:   int64(st.Ffree),
		nameMax:     int64(st.Namelen),
		pathMax:     linuxPathMax,
		fsType:      int64(st.Type),
		fsid:        fsidWord(st.Fsid.X__val),
		fragSize:    int64(st.Frsize),
	}, nil
}
