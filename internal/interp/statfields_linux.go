//go:build linux

package interp

import (
	"os"
	"syscall"
)

// statFields projects Linux's `struct stat` onto rawStat. Every field is
// present here, so nothing is left at its zero value except for a
// FileInfo that did not come from the OS at all.
func statFields(info os.FileInfo) rawStat {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return rawStat{}
	}
	return rawStat{
		mode:      st.Mode,
		nlink:     uint32(st.Nlink),
		uid:       st.Uid,
		gid:       st.Gid,
		dev:       int64(st.Dev),
		rdev:      int64(st.Rdev),
		ino:       int64(st.Ino),
		blksize:   int64(st.Blksize),
		blocks:    st.Blocks,
		atime:     st.Atim.Sec,
		atimeNsec: st.Atim.Nsec,
		mtime:     st.Mtim.Sec,
		mtimeNsec: st.Mtim.Nsec,
		ctime:     st.Ctim.Sec,
		ctimeNsec: st.Ctim.Nsec,
	}
}
