package interp

import "syscall"

func reactorCreate() (int, error) {
	return syscall.EpollCreate1(syscall.EPOLL_CLOEXEC)
}

func (r *reactor) watch(raw int, interest int) error {
	ev := syscall.EpollEvent{Fd: int32(raw)}
	if interest&1 != 0 {
		ev.Events |= syscall.EPOLLIN
	}
	if interest&2 != 0 {
		ev.Events |= syscall.EPOLLOUT
	}
	if interest == 0 {
		return syscall.EpollCtl(r.fd, syscall.EPOLL_CTL_DEL, raw, &ev)
	}
	err := syscall.EpollCtl(r.fd, syscall.EPOLL_CTL_ADD, raw, &ev)
	if err == syscall.EEXIST {
		err = syscall.EpollCtl(r.fd, syscall.EPOLL_CTL_MOD, raw, &ev)
	}
	return err
}

func (r *reactor) wait(cap int, timeoutMs int) ([]reactorEvent, error) {
	evs := make([]syscall.EpollEvent, cap)
	n, err := syscall.EpollWait(r.fd, evs, timeoutMs)
	if err != nil {
		return nil, err
	}
	out := make([]reactorEvent, n)
	for k := 0; k < n; k++ {
		var ready int64
		if evs[k].Events&syscall.EPOLLIN != 0 {
			ready |= 1
		}
		if evs[k].Events&syscall.EPOLLOUT != 0 {
			ready |= 2
		}
		if evs[k].Events&(syscall.EPOLLERR|syscall.EPOLLHUP) != 0 {
			ready |= 4
		}
		out[k] = reactorEvent{raw: int(evs[k].Fd), ready: ready}
	}
	return out, nil
}
