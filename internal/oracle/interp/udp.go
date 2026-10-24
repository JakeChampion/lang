package interp

import (
	"fmt"
	"net"
	"syscall"
)

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

func sockaddrOf(name string, addr Value, port int64) (syscall.Sockaddr, error) {
	ip, err := ipArg(name, addr)
	if err != nil {
		return nil, err
	}
	switch len(ip) {
	case 4:
		sa := &syscall.SockaddrInet4{Port: int(port)}
		copy(sa.Addr[:], ip)
		return sa, nil
	case 16:
		sa := &syscall.SockaddrInet6{Port: int(port)}
		copy(sa.Addr[:], ip)
		return sa, nil
	}
	return nil, nil
}

func senderInto(from Array, sa syscall.Sockaddr) {
	if len(from.E) < 19 {
		return
	}
	for k := 0; k < 19; k++ {
		from.E[k] = Number(0)
	}
	var port int
	switch a := sa.(type) {
	case *syscall.SockaddrInet4:
		from.E[0] = Number(4)
		for k, b := range a.Addr {
			from.E[1+k] = Number(b)
		}
		port = a.Port
	case *syscall.SockaddrInet6:
		from.E[0] = Number(6)
		for k, b := range a.Addr {
			from.E[1+k] = Number(b)
		}
		port = a.Port
	}
	from.E[17] = Number(port >> 8)
	from.E[18] = Number(port & 255)
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
	if len(args) != 2 {
		return nil, fmt.Errorf("udp_bind: expected 2 args, got %d", len(args))
	}
	return i.udpBind("udp_bind", args[0], args[1], false, false)
}

func builtinUdpBindWith(i *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("udp_bind_with: expected 3 args, got %d", len(args))
	}
	reuse, ok := args[2].(Bool)
	if !ok {
		return nil, fmt.Errorf("udp_bind_with: expected boolean reuse_port arg, got %T", args[2])
	}
	return i.udpBind("udp_bind_with", args[0], args[1], true, bool(reuse))
}

func (i *Interp) udpBind(name string, addr, port Value, dual, reuse bool) (Value, error) {
	n, err := numberArgs(name, []Value{port}, 1)
	if err != nil {
		return nil, err
	}
	sa, err := sockaddrOf(name, addr, n[0])
	if err != nil {
		return nil, err
	}
	family := syscall.AF_INET
	switch sa.(type) {
	case *syscall.SockaddrInet6:
		family = syscall.AF_INET6
	case nil:
		return Number(-int64(syscall.EAFNOSUPPORT)), nil
	}
	fd, err := syscall.Socket(family, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return negErrno(err), nil
	}
	if dual && family == syscall.AF_INET6 {
		err = syscall.SetsockoptInt(fd, ipprotoIPv6, ipv6V6only, 0)
	}
	if err == nil && reuse {
		err = syscall.SetsockoptInt(fd, solSocket, soReusePort, 1)
	}
	if err == nil {
		err = syscall.Bind(fd, sa)
	}
	if err != nil {
		syscall.Close(fd)
		return negErrno(err), nil
	}
	return i.newUdpHandle(fd), nil
}

func builtinUdpConnect(i *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("udp_connect: expected 3 args, got %d", len(args))
	}
	n, err := numberArgs("udp_connect", []Value{args[0], args[2]}, 2)
	if err != nil {
		return nil, err
	}
	sa, err := sockaddrOf("udp_connect", args[1], n[1])
	if err != nil {
		return nil, err
	}
	if sa == nil {
		return Number(-int64(syscall.EAFNOSUPPORT)), nil
	}
	fd, ok := i.udpSocks[n[0]]
	if !ok {
		return Number(-int64(syscall.EBADF)), nil
	}
	if err := syscall.Connect(fd, sa); err != nil {
		return negErrno(err), nil
	}
	return Number(0), nil
}

func builtinUdpSendto(i *Interp, args []Value) (Value, error) {
	return udpSendto(i, args, false)
}

func builtinUdpSendtoBytes(i *Interp, args []Value) (Value, error) {
	return udpSendto(i, args, true)
}

func udpPayload(name string, value Value) ([]byte, error) {
	array, ok := value.(Array)
	if !ok {
		return nil, fmt.Errorf("%s: expected u8[] data arg, got %T", name, value)
	}
	data := make([]byte, len(array.E))
	for k, value := range array.E {
		n, ok := value.(Number)
		if !ok {
			return nil, fmt.Errorf("%s: expected u8 data element, got %T", name, value)
		}
		data[k] = byte(n)
	}
	return data, nil
}

func udpSendto(i *Interp, args []Value, raw bool) (Value, error) {
	name := "udp_sendto"
	if raw {
		name = "udp_sendto_bytes"
	}
	if len(args) != 4 {
		return nil, fmt.Errorf("%s: expected 4 args, got %d", name, len(args))
	}
	n, err := numberArgs(name, []Value{args[0], args[2]}, 2)
	if err != nil {
		return nil, err
	}
	var data []byte
	if raw {
		data, err = udpPayload(name, args[3])
		if err != nil {
			return nil, err
		}
	} else {
		text, ok := args[3].(String)
		if !ok {
			return nil, fmt.Errorf("%s: expected string data arg, got %T", name, args[3])
		}
		data = []byte(text)
	}
	fd, ok := i.udpSocks[n[0]]
	if !ok {
		return Number(-int64(syscall.EBADF)), nil
	}
	var to syscall.Sockaddr
	if addr, ok := args[1].(Array); ok && len(addr.E) != 0 {
		to, err = sockaddrOf(name, args[1], n[1])
		if err != nil {
			return nil, err
		}
		if to == nil {
			return Number(-int64(syscall.EAFNOSUPPORT)), nil
		}
	} else if !ok {
		return nil, fmt.Errorf("%s: expected u8[] addr arg, got %T", name, args[1])
	}
	if err := syscall.Sendto(fd, data, 0, to); err != nil {
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
	senderInto(from, sa)
	return Number(n), nil
}

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
	switch a := sa.(type) {
	case *syscall.SockaddrInet4:
		return Number(a.Port), true
	case *syscall.SockaddrInet6:
		return Number(a.Port), true
	}
	return Number(-int64(syscall.EAFNOSUPPORT)), true
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
	case 9, 10:
		return nameGroup(fd, op, arg), true
	}
	if err != nil {
		return negErrno(err), true
	}
	return Number(0), true
}

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
