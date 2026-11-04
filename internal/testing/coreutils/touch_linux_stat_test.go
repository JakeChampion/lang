//go:build linux

package coreutils

import (
	"syscall"
	"time"
)

func statTimes(st *syscall.Stat_t) (atime, mtime time.Time) {
	return time.Unix(st.Atim.Sec, st.Atim.Nsec), time.Unix(st.Mtim.Sec, st.Mtim.Nsec)
}
