//go:build darwin

package e2e

import (
	"syscall"
	"testing"
)

func hostBirth(t *testing.T, path string) (int64, int64) {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return st.Birthtimespec.Sec, st.Birthtimespec.Nsec
}

func hostDevs(t *testing.T, path string) (int64, int64) {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return int64(st.Dev), int64(st.Rdev)
}
