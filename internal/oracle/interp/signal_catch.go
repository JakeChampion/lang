package interp

import (
	"io"
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
	return catchSignal("signal_catch", args, false)
}

// builtinSignalCatchInterrupting is signal_catch whose arrival also ends a
// blocking read with EINTR, as a handler installed without SA_RESTART does
// for a compiled program. The Go runtime restarts every call its own handler
// interrupts, so the interruption is made here instead: each arrival reaches
// the wake pipe, which a read on a pipe, terminal or socket waits on beside
// its own descriptor (waitReadable). A read of a regular file never blocks,
// so a compiled program's is not interrupted either.
func builtinSignalCatchInterrupting(_ *Interp, args []Value) (Value, error) {
	return catchSignal("signal_catch_interrupting", args, true)
}

func catchSignal(name string, args []Value, interrupting bool) (Value, error) {
	sig, ok, err := signalArg(name, args)
	if err != nil {
		return nil, err
	}
	if !ok {
		return Number(-22), nil // -EINVAL, as the kernel answers
	}
	caught.Lock()
	defer caught.Unlock()
	ch, already := caught.m[sig]
	if !already {
		ch = make(chan os.Signal, 1)
		caught.m[sig] = ch
	}
	if interrupting {
		signal.Stop(ch)
		if err := wakeOn(sig, ch); err != nil {
			return Number(errnoOf(err)), nil
		}
		return Number(0), nil
	}
	wakeOff(sig)
	signal.Notify(ch, sig)
	return Number(0), nil
}

// wake is the pipe the interrupting catches write a byte to on each arrival,
// and the os/signal channel each one is delivered on. A forwarding goroutine
// raises the signal's flag BEFORE it writes the byte, so a read the byte
// wakes finds the signal taken.
var wake = struct {
	sync.Mutex
	r, w *os.File
	m    map[syscall.Signal]chan os.Signal
}{m: map[syscall.Signal]chan os.Signal{}}

func wakeOn(sig syscall.Signal, flag chan os.Signal) error {
	wake.Lock()
	defer wake.Unlock()
	if wake.r == nil {
		r, w, err := os.Pipe()
		if err != nil {
			return err
		}
		wake.r, wake.w = r, w
	}
	if _, ok := wake.m[sig]; ok {
		return nil
	}
	in := make(chan os.Signal, 1)
	signal.Notify(in, sig)
	wake.m[sig] = in
	go func(w *os.File) {
		for s := range in {
			select {
			case flag <- s:
			default:
			}
			w.Write([]byte{1})
		}
	}(wake.w)
	return nil
}

// wakeOff stops `sig` reaching the wake pipe.
func wakeOff(sig syscall.Signal) {
	wake.Lock()
	defer wake.Unlock()
	if in, ok := wake.m[sig]; ok {
		signal.Stop(in)
		close(in)
		delete(wake.m, sig)
	}
}

// waitReadable blocks until `f` has something to read, or answers EINTR once
// an interrupting catch's signal has arrived since the last answer. With no
// interrupting catch held, or a descriptor no read of would block, it
// answers at once and the read itself waits.
func waitReadable(f *os.File) error {
	wake.Lock()
	armed := len(wake.m) > 0
	wr := wake.r
	wake.Unlock()
	if !armed {
		return nil
	}
	if fi, err := f.Stat(); err != nil || fi.Mode().IsRegular() || fi.IsDir() {
		return nil
	}
	fd, err := reactorCreate()
	if err != nil {
		return nil
	}
	defer syscall.Close(fd)
	r := &reactor{fd: fd}
	if r.watch(int(f.Fd()), 1) != nil || r.watch(int(wr.Fd()), 1) != nil {
		return nil
	}
	evs, err := r.wait(2, -1)
	if err != nil {
		return nil
	}
	for _, ev := range evs {
		if ev.raw == int(wr.Fd()) {
			var buf [64]byte
			syscall.SetNonblock(ev.raw, true)
			syscall.Read(ev.raw, buf[:])
			syscall.SetNonblock(ev.raw, false)
			return syscall.EINTR
		}
	}
	return nil
}

// interruptible is a reader whose Read first waits in waitReadable, so a
// blocking read can end with EINTR.
type interruptible struct{ f *os.File }

func (r interruptible) Read(p []byte) (int, error) {
	if err := waitReadable(r.f); err != nil {
		return 0, err
	}
	return r.f.Read(p)
}

// interruptibleReader wraps `r` when it is a descriptor.
func interruptibleReader(r io.Reader) io.Reader {
	if f, ok := r.(*os.File); ok {
		return interruptible{f}
	}
	return r
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
	wakeOff(sig)
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
