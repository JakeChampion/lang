//go:build linux

package interp

import (
	"syscall"
	"unsafe"
)

// hostSteerByCPU attaches the classic filter `ld cpu; ret a` to the
// socket's SO_REUSEPORT group (SO_ATTACH_REUSEPORT_CBPF), so a connection
// goes to the group's socket whose index is the CPU it arrived on.
func hostSteerByCPU(fd int) error {
	type sockFilter struct {
		code   uint16
		jt, jf uint8
		k      uint32
	}
	filter := [2]sockFilter{{code: 0x20, k: 0xfffff024}, {code: 0x16}}
	prog := struct {
		len    uint16
		_      [6]byte
		filter uintptr
	}{len: 2, filter: uintptr(unsafe.Pointer(&filter[0]))}
	const soAttachReuseportCBPF = 51
	_, _, e := syscall.Syscall6(syscall.SYS_SETSOCKOPT, uintptr(fd), syscall.SOL_SOCKET, soAttachReuseportCBPF, uintptr(unsafe.Pointer(&prog)), unsafe.Sizeof(prog), 0)
	if e != 0 {
		return e
	}
	return nil
}
