package interp

import (
	"os/signal"
	"syscall"
	"time"
)

func raiseSelf(sig syscall.Signal) error {
	if err := syscall.Kill(syscall.Getpid(), sig); err != nil {
		return err
	}
	if diesOf(sig) && !signal.Ignored(sig) && !isCaught(sig) {
		time.Sleep(time.Second)
	}
	return nil
}

func diesOf(sig syscall.Signal) bool {
	switch sig {
	case 0, syscall.SIGURG, syscall.SIGCONT, syscall.SIGCHLD, syscall.SIGWINCH, syscall.SIGINFO,
		syscall.SIGSTOP, syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU:
		return false
	}
	return true
}
