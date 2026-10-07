package interp

import (
	"runtime"
	"syscall"
)

// raiseSelf sends sig to the calling thread, so it is delivered before the
// call returns, as kill(2) of a one-thread process delivers it: the Go
// runtime's handler runs on this thread, and for a signal at its default
// disposition it ends the process there.
func raiseSelf(sig syscall.Signal) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return syscall.Tgkill(syscall.Getpid(), syscall.Gettid(), sig)
}
