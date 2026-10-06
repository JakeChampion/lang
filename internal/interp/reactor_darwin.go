package interp

import (
	"os"
	"syscall"
)

func reactorCreate() (int, error) {
	return syscall.Kqueue()
}

// kqueue keeps one entry per filter: EV_ADD with EV_ENABLE for a filter
// wanted, EV_DELETE for one not, where deleting an absent filter is
// ENOENT and not an error.
func (r *reactor) watch(raw int, interest int) error {
	filters := []struct {
		filter int16
		bit    int
	}{{syscall.EVFILT_READ, 1}, {syscall.EVFILT_WRITE, 2}}
	for _, f := range filters {
		flags := uint16(syscall.EV_DELETE)
		if interest&f.bit != 0 {
			flags = syscall.EV_ADD | syscall.EV_ENABLE
		}
		kev := syscall.Kevent_t{Ident: uint64(raw), Filter: f.filter, Flags: flags}
		if _, err := syscall.Kevent(r.fd, []syscall.Kevent_t{kev}, nil, nil); err != nil {
			if !(flags == syscall.EV_DELETE && err == syscall.ENOENT) {
				return err
			}
		}
	}
	return nil
}

func (r *reactor) wait(cap int, timeoutMs int) ([]reactorEvent, error) {
	evs := make([]syscall.Kevent_t, cap)
	var ts *syscall.Timespec
	if timeoutMs >= 0 {
		t := syscall.NsecToTimespec(int64(timeoutMs) * 1000000)
		ts = &t
	}
	n, err := syscall.Kevent(r.fd, nil, evs, ts)
	for err == syscall.EINTR {
		n, err = syscall.Kevent(r.fd, nil, evs, ts)
	}
	if err != nil {
		return nil, err
	}
	out := make([]reactorEvent, n)
	for k := 0; k < n; k++ {
		ready := int64(2)
		if evs[k].Filter == syscall.EVFILT_READ {
			ready = 1
		}
		if evs[k].Flags&(syscall.EV_EOF|syscall.EV_ERROR) != 0 {
			ready |= 4
		}
		out[k] = reactorEvent{raw: int(evs[k].Ident), ready: ready}
	}
	return out, nil
}

// watchParent raises SIGTERM, which the SIGTERM watch reports, once the
// parent exits: a kqueue of its own holds the EVFILT_PROC NOTE_EXIT. A
// parent already gone is ESRCH.
func watchParent() error {
	ppid := os.Getppid()
	if ppid == 1 {
		return syscall.ESRCH
	}
	kq, err := syscall.Kqueue()
	if err != nil {
		return err
	}
	ch := syscall.Kevent_t{Ident: uint64(ppid), Filter: syscall.EVFILT_PROC, Flags: syscall.EV_ADD | syscall.EV_ONESHOT, Fflags: syscall.NOTE_EXIT}
	if _, err := syscall.Kevent(kq, []syscall.Kevent_t{ch}, nil, nil); err != nil {
		syscall.Close(kq)
		return err
	}
	go func() {
		ev := make([]syscall.Kevent_t, 1)
		for {
			if _, err := syscall.Kevent(kq, nil, ev, nil); err != syscall.EINTR {
				break
			}
		}
		syscall.Close(kq)
		syscall.Kill(os.Getpid(), syscall.SIGTERM)
	}()
	return nil
}
