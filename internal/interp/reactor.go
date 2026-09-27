package interp

import (
	"errors"
	"fmt"
	"io"
	"net"
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
	// watched under.
	handles map[int]int64
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
		syscall.Close(r.fd)
		delete(i.reactors, n[0])
		return Number(0), nil
	case 1, 2:
		raw, ok := i.rawFd(n[2])
		if !ok {
			return Number(-int64(syscall.EBADF)), nil
		}
		interest := int(n[3]) & 3
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
		events.E[2*k] = Number(r.handles[e.raw])
		events.E[2*k+1] = Number(e.ready)
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
