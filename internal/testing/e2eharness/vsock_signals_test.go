//go:build linux

package e2eharness

import (
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// sleepUnderSignals sleeps 300 ms in one syscall on its own thread, through
// run, while SIGURG goes to that thread every 10 ms. It gives the errno of
// the sleep.
func sleepUnderSignals(t *testing.T, run func(func())) syscall.Errno {
	t.Helper()
	tids := make(chan int, 1)
	errs := make(chan syscall.Errno, 1)
	go func() {
		run(func() {
			tids <- syscall.Gettid()
			ts := syscall.Timespec{Nsec: 300 * int64(time.Millisecond)}
			_, _, e := syscall.Syscall(syscall.SYS_NANOSLEEP, uintptr(unsafe.Pointer(&ts)), 0, 0)
			errs <- e
		})
	}()
	tid := <-tids
	for {
		select {
		case e := <-errs:
			return e
		case <-time.After(10 * time.Millisecond):
			if err := syscall.Tgkill(syscall.Getpid(), tid, syscall.SIGURG); err != nil {
				t.Fatalf("tgkill: %v", err)
			}
		}
	}
}

func onLockedThread(f func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	f()
}

func TestWithSignalsBlockedKeepsASyscallWhole(t *testing.T) {
	if e := sleepUnderSignals(t, withSignalsBlocked); e != 0 {
		t.Fatalf("the sleep with each signal blocked ended with %v", e)
	}
}

func TestASignalEndsASleepThatDoesNotBlockIt(t *testing.T) {
	if e := sleepUnderSignals(t, onLockedThread); e != syscall.EINTR {
		t.Fatalf("the sleep without blocked signals ended with %v, want EINTR", e)
	}
}
