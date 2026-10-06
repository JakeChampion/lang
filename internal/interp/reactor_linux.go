package interp

import (
	"os"
	"syscall"
)

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
	if interest&4 != 0 {
		// EPOLLEXCLUSIVE: of the processes watching one descriptor, a
		// readiness wakes one.
		ev.Events |= 1 << 28
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
	for err == syscall.EINTR {
		n, err = syscall.EpollWait(r.fd, evs, timeoutMs)
	}
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

// watchParent has the kernel send SIGTERM when the parent exits, which the
// SIGTERM watch reports. A parent already gone is ESRCH.
func watchParent() error {
	if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_SET_PDEATHSIG, uintptr(syscall.SIGTERM), 0); e != 0 {
		return e
	}
	if os.Getppid() == 1 {
		return syscall.ESRCH
	}
	return nil
}
