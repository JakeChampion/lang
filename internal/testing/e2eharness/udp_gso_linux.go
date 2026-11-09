//go:build linux

package e2eharness

import (
	"encoding/binary"
	"net"
	"syscall"
	"unsafe"
)

// SendSegmented sends data to addr as one UDP_SEGMENT message that the kernel
// splits into datagrams of seg bytes, the last one shorter when data is not a
// multiple. A receiver with UDP_GRO on sees it as one message again.
func SendSegmented(conn *net.UDPConn, addr *net.UDPAddr, data []byte, seg int) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var to syscall.Sockaddr
	if ip4 := addr.IP.To4(); ip4 != nil {
		sa := &syscall.SockaddrInet4{Port: addr.Port}
		copy(sa.Addr[:], ip4)
		to = sa
	} else {
		sa := &syscall.SockaddrInet6{Port: addr.Port}
		copy(sa.Addr[:], addr.IP.To16())
		to = sa
	}
	oob := make([]byte, syscall.CmsgSpace(2))
	h := (*syscall.Cmsghdr)(unsafe.Pointer(&oob[0]))
	h.Level = syscall.IPPROTO_UDP
	h.Type = 103
	h.SetLen(syscall.CmsgLen(2))
	binary.LittleEndian.PutUint16(oob[syscall.CmsgLen(0):], uint16(seg))
	var sendErr error
	if err := raw.Control(func(fd uintptr) {
		_, sendErr = syscall.SendmsgN(int(fd), data, oob, to, 0)
	}); err != nil {
		return err
	}
	return sendErr
}

// SetTOS marks every datagram the socket sends with tos, in the IP header's
// TOS byte or the IPv6 traffic class.
func SetTOS(conn *net.UDPConn, v6 bool, tos int) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var setErr error
	if err := raw.Control(func(fd uintptr) {
		if v6 {
			setErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_TCLASS, tos)
		} else {
			setErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TOS, tos)
		}
	}); err != nil {
		return err
	}
	return setErr
}

// RecvTOS asks the socket for the TOS byte, or the IPv6 traffic class, of
// each datagram it receives.
func RecvTOS(conn *net.UDPConn, v6 bool) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var setErr error
	if err := raw.Control(func(fd uintptr) {
		if v6 {
			setErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_RECVTCLASS, 1)
		} else {
			setErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_RECVTOS, 1)
		}
	}); err != nil {
		return err
	}
	return setErr
}

// ReadTOS reads one datagram and the TOS byte RecvTOS asked for, 0 when the
// datagram carried none.
func ReadTOS(conn *net.UDPConn, buf []byte) (int, int, error) {
	oob := make([]byte, 64)
	n, oobn, _, _, err := conn.ReadMsgUDP(buf, oob)
	if err != nil {
		return 0, 0, err
	}
	msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return n, 0, err
	}
	for _, m := range msgs {
		if (m.Header.Level == syscall.IPPROTO_IP && m.Header.Type == syscall.IP_TOS || m.Header.Level == syscall.IPPROTO_IPV6 && m.Header.Type == syscall.IPV6_TCLASS) && len(m.Data) > 0 {
			return n, int(m.Data[0]), nil
		}
	}
	return n, 0, nil
}
