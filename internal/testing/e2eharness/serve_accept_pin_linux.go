//go:build linux

package e2eharness

import (
	"syscall"
	"unsafe"
)

// pinToCPU binds the calling OS thread to one CPU.
func pinToCPU(cpu int) error {
	var mask [16]uint64
	mask[cpu/64] = 1 << (cpu % 64)
	_, _, e := syscall.RawSyscall(syscall.SYS_SCHED_SETAFFINITY, 0, uintptr(len(mask)*8), uintptr(unsafe.Pointer(&mask[0])))
	if e != 0 {
		return e
	}
	return nil
}
