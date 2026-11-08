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
