//go:build darwin

package interp

import (
	"os"
	"syscall"
)

// statFields projects Darwin's 64-bit-inode `struct stat` onto rawStat.
// `st_mode` and `st_nlink` are 16-bit there and `st_dev` / `st_rdev` /
// `st_blksize` are 32-bit, so each widens; the timestamps are named
// `Atimespec` rather than Linux's `Atim`, which is why this file exists
// at all rather than one shared unix projection.
func statFields(info os.FileInfo) rawStat {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return rawStat{}
	}
	return rawStat{
		mode:      uint32(st.Mode),
		nlink:     uint32(st.Nlink),
		uid:       st.Uid,
		gid:       st.Gid,
		dev:       int64(st.Dev),
		rdev:      int64(st.Rdev),
		ino:       int64(st.Ino),
		blksize:   int64(st.Blksize),
		blocks:    st.Blocks,
		atime:     st.Atimespec.Sec,
		atimeNsec: st.Atimespec.Nsec,
		mtime:     st.Mtimespec.Sec,
		mtimeNsec: st.Mtimespec.Nsec,
		ctime:     st.Ctimespec.Sec,
		ctimeNsec: st.Ctimespec.Nsec,
	}
}
