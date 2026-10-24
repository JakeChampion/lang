//go:build linux

package interp

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

var sysStatx = map[bool]uintptr{true: 332, false: 291}[runtime.GOARCH == "amd64"]

func statFields(info os.FileInfo, at statOrigin) rawStat {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return rawStat{}
	}
	bt := birthTime(at)
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
		btime:     bt.sec,
		btimeNsec: bt.nsec,
	}
}

type birth struct{ sec, nsec int64 }

func birthTime(at statOrigin) birth {
	const (
		statxBtime      = 0x800
		atEmptyPath     = 0x1000
		atSymlinkNofoll = 0x100
		atFdcwd         = -100
	)
	dirfd := atFdcwd
	path := at.path
	flags := 0
	if at.file != nil {
		dirfd = int(at.file.Fd())
		path = ""
		flags = atEmptyPath
	} else if !at.follow {
		flags = atSymlinkNofoll
	}
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return birth{}
	}
	var buf [256]byte
	_, _, errno := syscall.Syscall6(sysStatx, uintptr(dirfd), uintptr(unsafe.Pointer(p)),
		uintptr(flags), statxBtime, uintptr(unsafe.Pointer(&buf[0])), 0)
	if errno != 0 || *(*uint32)(unsafe.Pointer(&buf[0]))&statxBtime == 0 {
		return birth{}
	}
	return birth{*(*int64)(unsafe.Pointer(&buf[80])), int64(*(*uint32)(unsafe.Pointer(&buf[88])))}
}
