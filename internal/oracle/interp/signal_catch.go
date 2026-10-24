package interp

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var caught = struct {
	sync.Mutex
	m map[syscall.Signal]chan os.Signal
}{m: map[syscall.Signal]chan os.Signal{}}

func builtinSignalCatch(_ *Interp, args []Value) (Value, error) {
	return catchSignal("signal_catch", args, false)
}

func builtinSignalCatchInterrupting(_ *Interp, args []Value) (Value, error) {
	return catchSignal("signal_catch_interrupting", args, true)
}

func catchSignal(name string, args []Value, interrupting bool) (Value, error) {
	sig, ok, err := signalArg(name, args)
	if err != nil {
		return nil, err
	}
	if !ok {
		return Number(-22), nil
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

func wakeOff(sig syscall.Signal) {
	wake.Lock()
	defer wake.Unlock()
	if in, ok := wake.m[sig]; ok {
		signal.Stop(in)
		close(in)
		delete(wake.m, sig)
	}
}

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

type interruptible struct{ f *os.File }

func (r interruptible) Read(p []byte) (int, error) {
	if err := waitReadable(r.f); err != nil {
		return 0, err
	}
	return r.f.Read(p)
}

func interruptibleReader(r io.Reader) io.Reader {
	if f, ok := r.(*os.File); ok {
		return interruptible{f}
	}
	return r
}

func builtinSignalRaise(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("signal_raise: expected 1 arg, got %d", len(args))
	}
	n, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("signal_raise: expected number arg, got %T", args[0])
	}
	if n < 0 || n > maxSignal {
		return Number(-22), nil
	}
	if n == 0 {
		return Number(0), nil
	}
	if err := raiseSelf(syscall.Signal(n)); err != nil {
		return Number(errnoOf(err)), nil
	}
	return Number(0), nil
}

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

func uncatch(sig syscall.Signal) {
	wakeOff(sig)
	caught.Lock()
	delete(caught.m, sig)
	caught.Unlock()
}

func isCaught(sig syscall.Signal) bool {
	caught.Lock()
	defer caught.Unlock()
	_, ok := caught.m[sig]
	return ok
}
