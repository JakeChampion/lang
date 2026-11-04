package coreutils

import (
	"syscall"
	"time"
)

func statTimes(st *syscall.Stat_t) (atime, mtime time.Time) {
	return time.Unix(st.Atimespec.Sec, st.Atimespec.Nsec), time.Unix(st.Mtimespec.Sec, st.Mtimespec.Nsec)
}
