//go:build linux && arm64

package arm64

import (
	"syscall"
	"testing"
)

// linuxDarwinSysno's Linux column and linuxOnlySysno are transcribed by hand
// from asm-generic/unistd.h, and Go's own zsysnum table is a second
// transcription of it. Comparing them catches a TRANSPOSITION, which is the
// one error in this table no behavioural test can see: fsync(2) and
// fdatasync(2) have identical error contracts — every descriptor that
// answers EINVAL for one answers EINVAL for the other, and both answer EBADF
// on a closed fd — so a program cannot tell which of the two ran. Their
// numbers are adjacent, which is exactly when a transposition happens.
//
// Only the Linux column is checkable: `syscall.SYS_*` is this host's, and
// the Darwin column is held by TestSelfHostArm64SysnoAgreesWithNative
// against the self-host's independent copy.
//
// It runs only where the test binary's own GOARCH matches the table. The
// x86-64 half lives in that backend.
func TestSyscallNumbersMatchTheKernelTable(t *testing.T) {
	pair := func(name string) int {
		t.Helper()
		n, ok := linuxDarwinSysno[name]
		if !ok {
			t.Fatalf("no linuxDarwinSysno row for %q — this gate has gone stale", name)
		}
		return n[0]
	}
	only := func(name string) int {
		t.Helper()
		n, ok := linuxOnlySysno[name]
		if !ok {
			t.Fatalf("no linuxOnlySysno row for %q — this gate has gone stale", name)
		}
		return n
	}
	for _, c := range []struct {
		name string
		got  int
		want uintptr
	}{
		// Anchors, so an emptied table fails rather than passes.
		{"read", pair("read"), syscall.SYS_READ},
		{"write", pair("write"), syscall.SYS_WRITE},
		// The adjacent pairs, which is what this is for.
		{"fsync", pair("fsync"), syscall.SYS_FSYNC},
		{"fdatasync", pair("fdatasync"), syscall.SYS_FDATASYNC},
		{"sync", pair("sync"), syscall.SYS_SYNC},
		{"syncfs", only("syncfs"), syscall.SYS_SYNCFS},
		{"ftruncate", pair("ftruncate"), syscall.SYS_FTRUNCATE},
	} {
		if uintptr(c.got) != c.want {
			t.Errorf("%s = %d, the kernel table says %d", c.name, c.got, c.want)
		}
	}
}
