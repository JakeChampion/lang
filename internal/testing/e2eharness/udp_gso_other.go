//go:build !linux

package e2eharness

import (
	"errors"
	"net"
)

func SendSegmented(conn *net.UDPConn, addr *net.UDPAddr, data []byte, seg int) error {
	return errors.New("UDP_SEGMENT needs Linux")
}

func SetTOS(conn *net.UDPConn, v6 bool, tos int) error {
	return errors.New("the TOS mark of this test needs Linux")
}
