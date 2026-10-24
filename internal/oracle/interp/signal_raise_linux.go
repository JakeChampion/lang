package interp

import (
	"runtime"
	"syscall"
)

func raiseSelf(sig syscall.Signal) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return syscall.Tgkill(syscall.Getpid(), syscall.Gettid(), sig)
}
