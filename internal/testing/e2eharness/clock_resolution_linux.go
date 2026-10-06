package e2eharness

import (
	"syscall"
	"testing"
	"unsafe"
)

// HostClockResolution is the host kernel's clock_getres(CLOCK_REALTIME) in
// nanoseconds.
func HostClockResolution(t testing.TB) int64 {
	t.Helper()
	var ts syscall.Timespec
	if _, _, errno := syscall.Syscall(syscall.SYS_CLOCK_GETRES, 0, uintptr(unsafe.Pointer(&ts)), 0); errno != 0 {
		t.Fatalf("clock_getres: %v", errno)
	}
	return ts.Sec*1_000_000_000 + ts.Nsec
}
