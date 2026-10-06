package e2eharness

import (
	"os"
	"syscall"
	"testing"
)

func copyByteOtherDevice(t *testing.T, source string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/dev/shm", "fern-copy-bytes-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	var src, dst syscall.Stat_t
	if err := syscall.Stat(source, &src); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Stat(dir, &dst); err != nil {
		t.Fatal(err)
	}
	if src.Dev == dst.Dev {
		t.Fatal("mv fixture must cross filesystem devices")
	}
	return dir
}
