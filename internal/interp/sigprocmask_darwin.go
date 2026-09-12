//go:build darwin

package interp

import (
	"syscall"
	"unsafe"
)

// sigprocmaskSwap is sigprocmask(2) in XNU's 3-argument form. `how`
// arrives in Fern's numbering, which XNU spells 1/2/3 where Linux writes
// 0/1/2, so one is added here — the same +1 the arm64 emitter applies, the
// case only Darwin can catch. XNU's sigset_t is 32 bytes with signals 1..32
// each at bit sig-1 of the first word, so the i64 mask lands in it directly;
// both buffers are a full zeroed 32 bytes so a kernel that writes the whole
// struct is contained.
func sigprocmaskSwap(how uintptr, mask int64) (int64, syscall.Errno) {
	var newset, oldset [32]byte
	*(*int64)(unsafe.Pointer(&newset[0])) = mask
	_, _, errno := syscall.Syscall6(syscall.SYS_SIGPROCMASK, how+1,
		uintptr(unsafe.Pointer(&newset[0])), uintptr(unsafe.Pointer(&oldset[0])), 0, 0, 0)
	if errno != 0 {
		return 0, errno
	}
	return *(*int64)(unsafe.Pointer(&oldset[0])), 0
}