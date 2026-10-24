//go:build !linux

package interp

func hostTimerFd(int64) (int, error) {
	return -1, nil
}
