package interp

import (
	"fmt"
	"net"
	"syscall"
)

// The datagram sockets (#9853) are raw descriptors from the syscall package
// rather than net.UDPConn values: a connect after the bind, a sendto on a
// connected socket and the kernel's own errno are what the native runtime
// bodies do, and the interpreter reports the same numbers. A handle shares
// the tcp id space, so tcp_close, tcp_local_port and tcp_socket_ctl take
// either kind.

func numberArgs(name string, args []Value, want int) ([]int64, error) {
	if len(args) != want {
		return nil, fmt.Errorf("%s: expected %d args, got %d", name, want, len(args))
	}
	out := make([]int64, want)
	for k, a := range args {
		n, ok := a.(Number)
		if !ok {
			return nil, fmt.Errorf("%s: expected number arg %d, got %T", name, k, a)
		}
		out[k] = int64(n)
	}
	return out, nil
}

// sockaddrV4 is the address the builtins pack in network order: the first
// octet in the low byte.
func sockaddrV4(hostBE, port int64) *syscall.SockaddrInet4 {
	return &syscall.SockaddrInet4{
		Port: int(port),
		Addr: [4]byte{byte(hostBE), byte(hostBE >> 8), byte(hostBE >> 16), byte(hostBE >> 24)},
	}
}

func (i *Interp) newUdpHandle(fd int) Value {
	if i.udpSocks == nil {
		i.udpSocks = map[int64]int{}
	}
	i.tcpNextHandle++
	i.udpSocks[i.tcpNextHandle] = fd
	return Number(i.tcpNextHandle)
}

func builtinUdpBind(i *Interp, args []Value) (Value, error) {
	n, err := numberArgs("udp_bind", args, 2)
	if err != nil {
		return nil, err
	}
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return negErrno(err), nil
	}
	if err := syscall.Bind(fd, sockaddrV4(n[0], n[1])); err != nil {
		syscall.Close(fd)
		return negErrno(err), nil
	}
	return i.newUdpHandle(fd), nil
}

func builtinUdpConnect(i *Interp, args []Value) (Value, error) {
	n, err := numberArgs("udp_connect", args, 3)
	if err != nil {
		return nil, err
	}
	fd, ok := i.udpSocks[n[0]]
	if !ok {
		return Number(-int64(syscall.EBADF)), nil
	}
	if err := syscall.Connect(fd, sockaddrV4(n[1], n[2])); err != nil {
		return negErrno(err), nil
	}
	return Number(0), nil
}

func builtinUdpSendto(i *Interp, args []Value) (Value, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("udp_sendto: expected 4 args, got %d", len(args))
	}
	n, err := numberArgs("udp_sendto", args[:3], 3)
	if err != nil {
		return nil, err
	}
	data, ok := args[3].(String)
	if !ok {
		return nil, fmt.Errorf("udp_sendto: expected string data arg, got %T", args[3])
	}
	fd, ok := i.udpSocks[n[0]]
	if !ok {
		return Number(-int64(syscall.EBADF)), nil
	}
	var to syscall.Sockaddr
	if n[1] != 0 || n[2] != 0 {
		to = sockaddrV4(n[1], n[2])
	}
	// A datagram goes out whole or not at all, so the count is the length.
	if err := syscall.Sendto(fd, []byte(data), 0, to); err != nil {
		return negErrno(err), nil
	}
	return Number(len(data)), nil
}

func builtinUdpRecvfrom(i *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("udp_recvfrom: expected 3 args, got %d", len(args))
	}
	id, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("udp_recvfrom: expected number fd arg, got %T", args[0])
	}
	buf, ok := args[1].(Array)
	if !ok {
		return nil, fmt.Errorf("udp_recvfrom: expected u8[] buf arg, got %T", args[1])
	}
	from, ok := args[2].(Array)
	if !ok {
		return nil, fmt.Errorf("udp_recvfrom: expected u8[] from arg, got %T", args[2])
	}
	fd, ok := i.udpSocks[int64(id)]
	if !ok {
		return Number(-int64(syscall.EBADF)), nil
	}
	bytes := make([]byte, len(buf.E))
	n, sa, err := syscall.Recvfrom(fd, bytes, 0)
	if err != nil {
		return negErrno(err), nil
	}
	for k := 0; k < n; k++ {
		buf.E[k] = Number(bytes[k])
	}
	if v4, ok := sa.(*syscall.SockaddrInet4); ok && len(from.E) >= 6 {
		for k := 0; k < 4; k++ {
			from.E[k] = Number(v4.Addr[k])
		}
		from.E[4] = Number(v4.Port >> 8)
		from.E[5] = Number(v4.Port & 255)
	}
	return Number(n), nil
}

// udpClose, udpLocalPort and udpSocketCtl are the datagram arms of the
// socket builtins that take any handle. Each answers false for a handle
// that is not a datagram socket.
func (i *Interp) udpClose(id int64) (Value, bool) {
	fd, ok := i.udpSocks[id]
	if !ok {
		return nil, false
	}
	delete(i.udpSocks, id)
	if err := syscall.Close(fd); err != nil {
		return negErrno(err), true
	}
	return Number(0), true
}

func (i *Interp) udpLocalPort(id int64) (Value, bool) {
	fd, ok := i.udpSocks[id]
	if !ok {
		return nil, false
	}
	sa, err := getsockname(fd)
	if err != nil {
		return negErrno(err), true
	}
	v4, ok := sa.(*syscall.SockaddrInet4)
	if !ok {
		return Number(-int64(syscall.EAFNOSUPPORT)), true
	}
	return Number(v4.Port), true
}

func (i *Interp) udpSocketCtl(id, op, arg int64) (Value, bool) {
	fd, ok := i.udpSocks[id]
	if !ok {
		return nil, false
	}
	var err error
	switch op {
	case 1:
		err = syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, tcpNodelay, int(arg))
	case 2:
		err = syscall.SetsockoptInt(fd, solSocket, soKeepalive, int(arg))
	case 3:
		err = syscall.SetNonblock(fd, arg != 0)
	case 4:
		err = syscall.Shutdown(fd, int(arg))
	case 5:
		err = connectResult(fd)
	}
	if err != nil {
		return negErrno(err), true
	}
	return Number(0), true
}

// The Unix-domain sockets go through the net package like the tcp ones,
// so tcp_accept, tcp_recv, tcp_send and tcp_close take them. A path longer
// than a sockaddr_un holds is refused with the natives' errno, where the
// net package would report EINVAL.
const sunPathMax = 107

func builtinUnixListen(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("unix_listen: expected 2 args, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("unix_listen: expected string path arg, got %T", args[0])
	}
	if _, ok := args[1].(Number); !ok {
		return nil, fmt.Errorf("unix_listen: expected number backlog arg, got %T", args[1])
	}
	if len(path) > sunPathMax {
		return Number(-int64(syscall.ENAMETOOLONG)), nil
	}
	ln, err := tcpNetListen("unix", string(path))
	if err != nil {
		return negErrno(err), nil
	}
	// The natives leave the socket file for the program to remove; the
	// net package would unlink it on close.
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	if i.tcpListeners == nil {
		i.tcpListeners = map[int64]tcpListenerHandle{}
	}
	i.tcpNextHandle++
	i.tcpListeners[i.tcpNextHandle] = ln
	return Number(i.tcpNextHandle), nil
}

func builtinUnixConnect(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("unix_connect: expected 1 arg, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("unix_connect: expected string path arg, got %T", args[0])
	}
	if len(path) > sunPathMax {
		return Number(-int64(syscall.ENAMETOOLONG)), nil
	}
	conn, err := net.Dial("unix", string(path))
	if err != nil {
		return negErrno(err), nil
	}
	if i.tcpConns == nil {
		i.tcpConns = map[int64]tcpConnHandle{}
	}
	i.tcpNextHandle++
	i.tcpConns[i.tcpNextHandle] = conn
	return Number(i.tcpNextHandle), nil
}
