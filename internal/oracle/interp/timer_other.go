//go:build !linux

package interp

// hostTimerFd: no timerfd here, so the answer is -1.
func hostTimerFd(int64) (int, error) {
	return -1, nil
}
