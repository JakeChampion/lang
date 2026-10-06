package interp

import (
	"os/signal"
	"syscall"
	"time"
)

// raiseSelf sends sig to the process. XNU hands a process-directed signal to
// any of the Go runtime's threads, so the handler may run after kill returns;
// for one that ends the process, the call waits for that rather than let the
// program run on past a raise a compiled program would not return from.
func raiseSelf(sig syscall.Signal) error {
	if err := syscall.Kill(syscall.Getpid(), sig); err != nil {
		return err
	}
	if diesOf(sig) && !signal.Ignored(sig) && !isCaught(sig) {
		time.Sleep(time.Second)
	}
	return nil
}

// diesOf reports whether sig's default action ends the process: everything
// but SIGURG, SIGCONT, SIGCHLD, SIGWINCH and SIGINFO, which are ignored, and
// the four that stop it.
func diesOf(sig syscall.Signal) bool {
	switch sig {
	case 0, syscall.SIGURG, syscall.SIGCONT, syscall.SIGCHLD, syscall.SIGWINCH, syscall.SIGINFO,
		syscall.SIGSTOP, syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU:
		return false
	}
	return true
}
