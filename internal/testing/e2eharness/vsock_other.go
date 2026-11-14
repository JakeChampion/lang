//go:build !linux

package e2eharness

func VsockConnectErrno(cid, port uint32) (int, bool) {
	return 0, false
}
