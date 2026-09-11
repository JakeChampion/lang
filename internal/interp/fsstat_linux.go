//go:build linux

package interp

import "syscall"

// fsStatFields is one `statfs(2)`: Linux reports the whole record, including
// `f_namelen`. There is no pathconf syscall — glibc computes it from statfs
// plus constants — so PATH_MAX is the kernel's own 4096, which is what
// `getconf PATH_MAX` reports for every Linux filesystem.
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
	}, nil
}
