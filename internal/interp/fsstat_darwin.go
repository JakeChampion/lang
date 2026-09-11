//go:build darwin

package interp

import "syscall"

// fsStatFields on Darwin takes three calls, because its `struct statfs` has
// the counts and NOT the name length: `statfs(2)` for the geometry, then
// `pathconf(2)` — a real syscall here, where Linux has none — for each limit.
//
// Not constants. APFS and HFS+ both answer 255 today, but a mounted FAT or
// SMB volume does not, and `pathchk` is the caller that would notice.
func fsStatFields(path string) (rawFsStat, error) {
	const (
		pcNameMax = 4
		pcPathMax = 5
	)
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return rawFsStat{}, err
	}
	nameMax, err := syscall.Pathconf(path, pcNameMax)
	if err != nil {
		return rawFsStat{}, err
	}
	pathMax, err := syscall.Pathconf(path, pcPathMax)
	if err != nil {
		return rawFsStat{}, err
	}
	return rawFsStat{
		blockSize:   int64(st.Bsize),
		blocks:      int64(st.Blocks),
		blocksFree:  int64(st.Bfree),
		blocksAvail: int64(st.Bavail),
		files:       int64(st.Files),
		filesFree:   int64(st.Ffree),
		nameMax:     int64(nameMax),
		pathMax:     int64(pathMax),
	}, nil
}
