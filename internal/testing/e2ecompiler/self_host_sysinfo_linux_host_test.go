//go:build linux

package e2ecompiler

import (
	"math/bits"
	"syscall"
	"testing"
	"unsafe"
)

func selfHostUtsname(t *testing.T) [5]string {
	t.Helper()
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		t.Fatalf("uname: %v", err)
	}
	str := func(f []int8) string {
		b := make([]byte, 0, len(f))
		for _, c := range f {
			if c == 0 {
				break
			}
			b = append(b, byte(c))
		}
		return string(b)
	}
	return [5]string{str(u.Sysname[:]), str(u.Nodename[:]), str(u.Release[:]), str(u.Version[:]), str(u.Machine[:])}
}

// selfHostCPUCount reads the affinity mask now. runtime.NumCPU reads it once,
// when the test process starts.
func selfHostCPUCount(t *testing.T) int {
	t.Helper()
	var mask [16]uint64
	_, _, errno := syscall.RawSyscall(syscall.SYS_SCHED_GETAFFINITY, 0, unsafe.Sizeof(mask), uintptr(unsafe.Pointer(&mask)))
	if errno != 0 {
		t.Fatalf("sched_getaffinity: %v", errno)
	}
	n := 0
	for _, w := range mask {
		n += bits.OnesCount64(w)
	}
	return n
}
