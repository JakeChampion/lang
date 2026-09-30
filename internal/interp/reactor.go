package interp

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// The reactor floor (#9853) in the interpreter: an epoll or kqueue set
// over the raw descriptors behind the tcp, unix and udp handles, so a
// program that waits on the reactor sees the same readiness the native
// runtime bodies report, with the handle the program knows in each event.

type reactor struct {
	fd int
	// handles maps a raw descriptor in the set to the handle it was
	// watched under; a signal's pipe maps to -signal.
	handles map[int]int64
	// signals holds the pipes that turn a delivered signal into readiness.
	signals map[int]*signalWatch
}

// signalWatch is one watched signal: os/signal delivers to ch, a
// goroutine writes a byte per delivery into the pipe, and the pipe's read
// end sits in the set under -signal.
type signalWatch struct {
	r, w *os.File
	ch   chan os.Signal
}

// watchSignal makes signal sig a readiness event: the pipe's read end,
// which reactor_ctl answers as the descriptor an unwatch names.
func (r *reactor) watchSignal(sig int) (int, error) {
	if r.signals == nil {
		r.signals = map[int]*signalWatch{}
	}
	if w, ok := r.signals[sig]; ok {
		return int(w.r.Fd()), nil
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	raw := int(pr.Fd())
	if err := syscall.SetNonblock(raw, true); err != nil {
		pr.Close()
		pw.Close()
		return 0, err
	}
	if err := r.watch(raw, 1); err != nil {
		pr.Close()
		pw.Close()
		return 0, err
	}
	w := &signalWatch{r: pr, w: pw, ch: make(chan os.Signal, 8)}
	signal.Notify(w.ch, syscall.Signal(sig))
	go func() {
		for range w.ch {
			w.w.Write([]byte{1})
		}
	}()
	r.signals[sig] = w
	r.handles[raw] = -int64(sig)
	return raw, nil
}

func (r *reactor) unwatchSignal(sig int) error {
	w, ok := r.signals[sig]
	if !ok {
		return syscall.EINVAL
	}
	signal.Stop(w.ch)
	close(w.ch)
	raw := int(w.r.Fd())
	err := r.watch(raw, 0)
	delete(r.handles, raw)
	delete(r.signals, sig)
	w.r.Close()
	w.w.Close()
	return err
}

// drainSignal consumes the deliveries queued in a signal's pipe.
func (r *reactor) drainSignal(raw int) {
	var buf [128]byte
	syscall.Read(raw, buf[:])
}

func errnoOf(err error) int64 {
	var en syscall.Errno
	if errors.As(err, &en) {
		return -int64(en)
	}
	return -int64(syscall.EIO)
}

// rawFd is the descriptor behind a socket handle of any kind.
func (i *Interp) rawFd(handle int64) (int, bool) {
	var sc syscall.Conn
	if ln, ok := i.tcpListeners[handle]; ok {
		if c, ok := ln.(syscall.Conn); ok {
			sc = c
		}
	} else if conn, ok := i.tcpConns[handle]; ok {
		if c, ok := conn.(syscall.Conn); ok {
			sc = c
		}
	} else if fd, ok := i.udpSocks[handle]; ok {
		return fd, true
	}
	if sc == nil {
		return 0, false
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return 0, false
	}
	raw := -1
	rc.Control(func(fd uintptr) { raw = int(fd) })
	return raw, raw >= 0
}

func builtinReactorNew(i *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("reactor_new: expected 0 args, got %d", len(args))
	}
	fd, err := reactorCreate()
	if err != nil {
		return Number(errnoOf(err)), nil
	}
	if i.reactors == nil {
		i.reactors = map[int64]*reactor{}
	}
	i.reactorNext++
	i.reactors[i.reactorNext] = &reactor{fd: fd, handles: map[int]int64{}}
	return Number(i.reactorNext), nil
}

func builtinReactorCtl(i *Interp, args []Value) (Value, error) {
	n, err := numberArgs("reactor_ctl", args, 4)
	if err != nil {
		return nil, err
	}
	r, ok := i.reactors[n[0]]
	if !ok {
		return Number(-int64(syscall.EBADF)), nil
	}
	switch n[1] {
	case 3:
		for sig := range r.signals {
			r.unwatchSignal(sig)
		}
		syscall.Close(r.fd)
		delete(i.reactors, n[0])
		return Number(0), nil
	case 4:
		raw, err := r.watchSignal(int(n[2]))
		if err != nil {
			return Number(errnoOf(err)), nil
		}
		return Number(raw), nil
	case 5:
		if err := r.unwatchSignal(int(n[2])); err != nil {
			return Number(errnoOf(err)), nil
		}
		return Number(0), nil
	case 1, 2:
		raw, ok := i.rawFd(n[2])
		if !ok {
			return Number(-int64(syscall.EBADF)), nil
		}
		interest := int(n[3]) & 7
		if n[1] == 2 {
			interest = 0
		}
		if err := r.watch(raw, interest); err != nil {
			return Number(errnoOf(err)), nil
		}
		if interest == 0 {
			delete(r.handles, raw)
		} else {
			r.handles[raw] = n[2]
		}
		return Number(0), nil
	}
	return Number(-int64(syscall.EINVAL)), nil
}

// A readiness event: the handle and its readiness bits, 1 readable, 2
// writable, 4 an error or hang-up.
type reactorEvent struct {
	raw   int
	ready int64
}

func builtinReactorWait(i *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("reactor_wait: expected 3 args, got %d", len(args))
	}
	id, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("reactor_wait: expected number arg 0, got %T", args[0])
	}
	events, ok := args[1].(Array)
	if !ok {
		return nil, fmt.Errorf("reactor_wait: expected array arg 1, got %T", args[1])
	}
	timeout, ok := args[2].(Number)
	if !ok {
		return nil, fmt.Errorf("reactor_wait: expected number arg 2, got %T", args[2])
	}
	r, ok := i.reactors[int64(id)]
	if !ok {
		return Number(-int64(syscall.EBADF)), nil
	}
	cap := len(events.E) / 2
	if cap <= 0 {
		return Number(-int64(syscall.EINVAL)), nil
	}
	got, err := r.wait(cap, int(timeout))
	if err != nil {
		return Number(errnoOf(err)), nil
	}
	for k, e := range got {
		handle := r.handles[e.raw]
		ready := e.ready
		if handle < 0 {
			r.drainSignal(e.raw)
			ready = 1
		}
		events.E[2*k] = Number(handle)
		events.E[2*k+1] = Number(ready)
	}
	return Number(len(got)), nil
}

// builtinTcpRecvInto reads once into the caller's buffer: the byte count,
// 0 at EOF, or -errno; a non-blocking connection with nothing to read
// answers -EAGAIN.
func builtinTcpRecvInto(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("tcp_recv_into: expected 2 args, got %d", len(args))
	}
	id, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_recv_into: expected number fd arg, got %T", args[0])
	}
	arr, ok := args[1].(Array)
	if !ok {
		return nil, fmt.Errorf("tcp_recv_into: expected array buf arg, got %T", args[1])
	}
	conn, ok := i.tcpConns[int64(id)]
	if !ok {
		return Number(-int64(syscall.EBADF)), nil
	}
	if len(arr.E) == 0 {
		return Number(0), nil
	}
	buf := make([]byte, len(arr.E))
	n, err := readSocket(conn, buf, i.tcpNonblocking[int64(id)])
	for j := 0; j < n; j++ {
		arr.E[j] = Number(buf[j])
	}
	if n > 0 {
		return Number(n), nil
	}
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return Number(0), nil
	}
	return Number(errnoOf(err)), nil
}

// readSocket is one read of a connection. A non-blocking one reads through
// the descriptor itself, so what is queued comes back and nothing answers
// EAGAIN; the deadline the net package offers would refuse to read at all
// once it has passed.
// writeSocket is readSocket's write side: one write(2) on the descriptor
// for a non-blocking socket, which answers a short count or EAGAIN
// rather than waiting for room.
func writeSocket(conn net.Conn, data []byte, nonblocking bool) (int, error) {
	if !nonblocking {
		return conn.Write(data)
	}
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return 0, syscall.EBADF
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return 0, err
	}
	n, werr := 0, error(nil)
	if err := rc.Write(func(fd uintptr) bool {
		n, werr = syscall.Write(int(fd), data)
		return true
	}); err != nil {
		return 0, err
	}
	if werr != nil {
		return 0, werr
	}
	return n, nil
}

func readSocket(conn net.Conn, buf []byte, nonblocking bool) (int, error) {
	if !nonblocking {
		conn.SetReadDeadline(time.Time{})
		return conn.Read(buf)
	}
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return 0, syscall.EBADF
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return 0, err
	}
	n, rerr := 0, error(nil)
	if err := rc.Read(func(fd uintptr) bool {
		n, rerr = syscall.Read(int(fd), buf)
		return true
	}); err != nil {
		return 0, err
	}
	if rerr != nil {
		return 0, rerr
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}
