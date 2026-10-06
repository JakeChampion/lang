//go:build unix

package interp

import "syscall"

func hostSetProcessGroup(pid, pgid int) error { return syscall.Setpgid(pid, pgid) }
