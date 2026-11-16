//go:build linux

package coreutils

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// pinLinkAtime sets the times of the link at path, not of its target.
func pinLinkAtime(path string, atime, mtime time.Time) error {
	const atFdcwd, atSymlinkNofollow = -100, 0x100
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	times := [2]syscall.Timespec{syscall.NsecToTimespec(atime.UnixNano()), syscall.NsecToTimespec(mtime.UnixNano())}
	fd := atFdcwd
	_, _, errno := syscall.Syscall6(syscall.SYS_UTIMENSAT, uintptr(fd),
		uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&times)), atSymlinkNofollow, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// A case that reads a link of the shared tree must not move its access time,
// because a parallel case can print that time on each side.
func TestFindSharedTreeLinkReadKeepsAtime(t *testing.T) {
	dir := findSharedTree(t)
	links := 0
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		links++
		var before, after syscall.Stat_t
		if err := syscall.Lstat(p, &before); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Readlink(p); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Lstat(p, &after); err != nil {
			t.Fatal(err)
		}
		a, _ := statTimes(&before)
		b, _ := statTimes(&after)
		if !a.Equal(b) {
			t.Errorf("reading the link %s moved its access time from %v to %v", p, a, b)
		}
		return nil
	})
	if links == 0 {
		t.Fatal("the shared tree has no links")
	}
}
