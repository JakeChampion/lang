package interp

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// caught holds the channel signal_catch registered for each signal. Its one
// slot IS the flag the compiled handler sets: os/signal never blocks on a
// send, so arrivals past the first coalesce into it, and signal_taken's
// non-blocking receive reads and clears it in one step.
//
// Delivery here is asynchronous where a compiled program's is not: a signal
// a program sends itself reaches the channel through the Go runtime's own
// signal goroutine, so it may be taken one poll later than kill(2) returning
// would suggest. A program that polls — the only contract signal_taken
// offers — sees it either way.
var caught = struct {
	sync.Mutex
	m map[syscall.Signal]chan os.Signal
}{m: map[syscall.Signal]chan os.Signal{}}

// builtinSignalCatch installs the flag-raising disposition for one signal.
// The interpreter shares its process with the Go runtime, which already
// resumes an interrupted system call, so SA_RESTART needs no counterpart.
func builtinSignalCatch(_ *Interp, args []Value) (Value, error) {
	sig, ok, err := signalArg("signal_catch", args)
	if err != nil {
		return nil, err
	}
	if !ok {
		return Number(-22), nil // -EINVAL, as the kernel answers
	}
	caught.Lock()
	defer caught.Unlock()
	if _, already := caught.m[sig]; !already {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, sig)
		caught.m[sig] = ch
	}
	return Number(0), nil
}

// builtinSignalTaken reports whether `sig` arrived since the last poll, and
// clears it. A signal nothing caught, or a number out of range, was never
// taken.
func builtinSignalTaken(_ *Interp, args []Value) (Value, error) {
	sig, ok, err := signalArg("signal_taken", args)
	if err != nil {
		return nil, err
	}
	if !ok {
		return Bool(false), nil
	}
	caught.Lock()
	ch := caught.m[sig]
	caught.Unlock()
	if ch == nil {
		return Bool(false), nil
	}
	select {
	case <-ch:
		return Bool(true), nil
	default:
		return Bool(false), nil
	}
}

// uncatch forgets the catch on `sig`, which signal.Ignore and signal.Reset
// have already dropped from os/signal's own table.
func uncatch(sig syscall.Signal) {
	caught.Lock()
	delete(caught.m, sig)
	caught.Unlock()
}

// isCaught reports whether signal_catch holds `sig`.
func isCaught(sig syscall.Signal) bool {
	caught.Lock()
	defer caught.Unlock()
	_, ok := caught.m[sig]
	return ok
}
