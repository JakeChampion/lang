//go:build linux

package e2e

import (
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

func hostBirth(t *testing.T, path string) (int64, int64) {
	t.Helper()
	sysStatx := uintptr(291)
	if runtime.GOARCH == "amd64" {
		sysStatx = 332
	}
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	var buf [256]byte
	const statxBtime = 0x800
	if _, _, errno := syscall.Syscall6(sysStatx, ^uintptr(99), uintptr(unsafe.Pointer(p)), 0, statxBtime,
		uintptr(unsafe.Pointer(&buf[0])), 0); errno != 0 {
		t.Fatalf("statx %s: %v", path, errno)
	}
	if *(*uint32)(unsafe.Pointer(&buf[0]))&statxBtime == 0 {
		return 0, 0
	}
	return *(*int64)(unsafe.Pointer(&buf[80])), int64(*(*uint32)(unsafe.Pointer(&buf[88])))
}

func hostDevs(t *testing.T, path string) (int64, int64) {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return int64(st.Dev), int64(st.Rdev)
}
