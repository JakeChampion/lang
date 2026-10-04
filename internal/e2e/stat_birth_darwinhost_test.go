//go:build darwin

package e2e

import (
	"syscall"
	"testing"
)

// hostBirth is st_birthtimespec, what a FileStat's btime / btime_nsec must
// read.
func hostBirth(t *testing.T, path string) (int64, int64) {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return st.Birthtimespec.Sec, st.Birthtimespec.Nsec
}

// hostDevs is (st_dev, st_rdev) as the host's own stat(2) reports them,
// sign-extended from Darwin's 32-bit dev_t as FileStat's are.
func hostDevs(t *testing.T, path string) (int64, int64) {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return int64(st.Dev), int64(st.Rdev)
}
