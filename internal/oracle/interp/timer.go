package interp

import (
	"syscall"
)

// builtinTimerFd answers `timer_fd(ms)`: a CLOCK_MONOTONIC timer that becomes
// readable once, `ms` milliseconds from now, as a handle poll and tcp_close
// take; -errno when the host refuses. A host without timerfd answers -1, as
// the arm64-darwin runtime does.
func builtinTimerFd(i *Interp, args []Value) (Value, error) {
	ms, err := numberArgs("timer_fd", args, 1)
	if err != nil {
		return nil, err
	}
	fd, err := hostTimerFd(ms[0])
	if err != nil {
		return negErrno(err), nil
	}
	if fd < 0 {
		return Number(-1), nil
	}
	if i.timerFds == nil {
		i.timerFds = map[int64]int{}
	}
	i.tcpNextHandle++
	i.timerFds[i.tcpNextHandle] = fd
	return Number(i.tcpNextHandle), nil
}

func (i *Interp) timerClose(id int64) (Value, bool) {
	fd, ok := i.timerFds[id]
	if !ok {
		return nil, false
	}
	delete(i.timerFds, id)
	if err := syscall.Close(fd); err != nil {
		return negErrno(err), true
	}
	return Number(0), true
}
