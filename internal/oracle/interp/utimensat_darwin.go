//go:build darwin

package interp

import (
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	utimeOmit = 1<<30 - 2
	utimeNow  = 1<<30 - 1
)

func setFileTimes(path string, times *[2]syscall.Timespec, nofollow bool) error {
	return setDarwinTimes(path, 0, times, nofollow, false)
}

func setFdTimes(fd int, times *[2]syscall.Timespec) error {
	return setDarwinTimes("", fd, times, false, true)
}

func setDarwinTimes(path string, fd int, times *[2]syscall.Timespec, nofollow, descriptor bool) error {
	var p *byte
	if !descriptor {
		var err error
		p, err = syscall.BytePtrFromString(path)
		if err != nil {
			return err
		}
	}
	if !descriptor && strings.HasSuffix(path, "/") {
		var stat syscall.Stat_t
		if err := syscall.Stat(path, &stat); err != nil {
			return err
		}
		if stat.Mode&syscall.S_IFMT != syscall.S_IFDIR {
			return syscall.ENOTDIR
		}
	}
	const (
		sysFsetattrlist  = 229
		sysSetattrlistat = 524
		attrModtime      = 0x400
		attrAcctime      = 0x1000
		fsoptNofollow    = 1
		fsoptUtimesNull  = 0x400
	)
	attrs := struct {
		bitmapCount uint16
		reserved    uint16
		common      uint32
		volume      uint32
		directory   uint32
		file        uint32
		fork        uint32
	}{bitmapCount: 5}
	var options uintptr
	if nofollow {
		options |= fsoptNofollow
	}
	if !descriptor && times[0].Nsec == utimeNow && times[1].Nsec == utimeNow {
		options |= fsoptUtimesNull
	}
	var now syscall.Timespec
	if times[0].Nsec == utimeNow || times[1].Nsec == utimeNow {
		t := time.Now()
		now = syscall.Timespec{Sec: t.Unix(), Nsec: int64(t.Nanosecond())}
	}
	var values [2]syscall.Timespec
	count := 0
	for _, i := range [...]int{1, 0} {
		stamp := times[i]
		switch stamp.Nsec {
		case utimeOmit:
			continue
		case utimeNow:
			stamp = now
		default:
			if stamp.Nsec < 0 || stamp.Nsec >= 1_000_000_000 {
				return syscall.EINVAL
			}
		}
		if i == 1 {
			attrs.common |= attrModtime
		} else {
			attrs.common |= attrAcctime
		}
		values[count] = stamp
		count++
	}
	var errno syscall.Errno
	if descriptor {
		_, _, errno = syscall.Syscall6(sysFsetattrlist, uintptr(fd),
			uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&values[0])),
			uintptr(count)*unsafe.Sizeof(values[0]), 0, 0)
	} else {
		dirfd := -2
		_, _, errno = syscall.Syscall6(sysSetattrlistat, uintptr(dirfd),
			uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&attrs)),
			uintptr(unsafe.Pointer(&values[0])), uintptr(count)*unsafe.Sizeof(values[0]), options)
	}
	if errno != 0 {
		return errno
	}
	return nil
}
