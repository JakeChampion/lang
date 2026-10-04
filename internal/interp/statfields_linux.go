//go:build linux

package interp

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// sysStatx is statx(2): x86-64's own table numbers it 332, the
// asm-generic one (arm64, riscv64) 291. `syscall` predates it.
var sysStatx = map[bool]uintptr{true: 332, false: 291}[runtime.GOARCH == "amd64"]

// statFields projects Linux's `struct stat` onto rawStat, and asks statx(2)
// for the birth time `struct stat` has no field for. Every field is present
// here, so nothing is left at its zero value except for a FileInfo that did
// not come from the OS at all, or a filesystem that records no birth time.
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

// birthTime is statx(2)'s stx_btime for what `at` names, or zero when the
// filesystem does not record one (STATX_BTIME clear in stx_mask).
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
