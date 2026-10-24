//go:build darwin

package interp

import (
	"os"
	"syscall"
)

func statFields(info os.FileInfo, _ statOrigin) rawStat {
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
		btime:     st.Birthtimespec.Sec,
		btimeNsec: st.Birthtimespec.Nsec,
	}
}
