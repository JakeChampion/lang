//go:build darwin

package interp

import (
	"syscall"
	"time"
	"unsafe"
)

const (
	utimeOmit = 1<<30 - 2
	utimeNow  = 1<<30 - 1
)

// setFileTimes uses setattrlistat, as the native Darwin runtime does.
// Leaving an attribute out preserves it atomically; reading its old value
// and writing it back would overwrite concurrent timestamp changes.
func setFileTimes(path string, times *[2]syscall.Timespec, nofollow bool) error {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	const (
		sysSetattrlistat = 524
		attrModtime      = 0x400
		attrAcctime      = 0x1000
		fsoptNofollow    = 1
		fsoptUtimesNull  = 0x40
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
	if times[0].Nsec == utimeNow && times[1].Nsec == utimeNow {
		// Both now requires write access, rather than ownership.
		options |= fsoptUtimesNull
	}
	var now syscall.Timespec
	if times[0].Nsec == utimeNow || times[1].Nsec == utimeNow {
		t := time.Now()
		now = syscall.Timespec{Sec: t.Unix(), Nsec: int64(t.Nanosecond())}
	}
	var values [2]syscall.Timespec
	count := 0
	// Attributes are packed in ascending bit order: mtime before atime.
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
	dirfd := -2 // AT_FDCWD
	_, _, errno := syscall.Syscall6(sysSetattrlistat, uintptr(dirfd),
		uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&attrs)),
		uintptr(unsafe.Pointer(&values[0])), uintptr(count)*unsafe.Sizeof(values[0]), options)
	if errno != 0 {
		return errno
	}
	return nil
}
