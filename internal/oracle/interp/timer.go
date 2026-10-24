package interp

import (
	"syscall"
)

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
