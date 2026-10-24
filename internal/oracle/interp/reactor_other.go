//go:build !linux && !darwin

package interp

import "syscall"

func reactorCreate() (int, error) { return 0, syscall.ENOSYS }

func (r *reactor) watch(raw int, interest int) error { return syscall.ENOSYS }

func (r *reactor) wait(cap int, timeoutMs int) ([]reactorEvent, error) {
	return nil, syscall.ENOSYS
}

func watchParent() error { return syscall.ENOSYS }
