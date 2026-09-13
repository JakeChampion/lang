//go:build linux && amd64

package x86_64

import (
	"syscall"
	"testing"
)

// The numbers above are transcribed by hand from
// arch/x86/entry/syscalls/syscall_64.tbl, and Go's own zsysnum table is a
// second transcription of the same kernel source. Comparing them catches a
// TRANSPOSITION, which is the one error in this table no behavioural test
// can see: fsync(2) and fdatasync(2) have identical error contracts — every
// descriptor that answers EINVAL for one answers EINVAL for the other, and
// both answer EBADF on a closed fd — so a program cannot tell which of the
// two ran. Their numbers are adjacent, which is exactly when a transposition
// happens.
//
// It runs only where the test binary's own GOARCH matches the table, since
// `syscall.SYS_*` is the host's. The arm64 half lives in that backend.
//
// syscall.SYS_SYNCFS is deliberately absent: Go's amd64 zsysnum stops at
// SYS_PRLIMIT64 = 302, so there is no constant to compare 306 against. The
// arm64 table does carry it, and covers it there.
func TestSyscallNumbersMatchTheKernelTable(t *testing.T) {
	for _, c := range []struct {
		name string
		got  int
		want uintptr
	}{
		// Anchors: a rename here that silently emptied the list would
		// otherwise make the whole gate vacuous.
		{"read", sysRead, syscall.SYS_READ},
		{"write", sysWrite, syscall.SYS_WRITE},
		// The adjacent pairs, which is what this is for.
		{"fsync", sysFsync, syscall.SYS_FSYNC},
		{"fdatasync", sysFdatasync, syscall.SYS_FDATASYNC},
		{"sync", sysSync, syscall.SYS_SYNC},
		{"fstat", sysFstat, syscall.SYS_FSTAT},
		{"lseek", sysLseek, syscall.SYS_LSEEK},
		{"close", sysClose, syscall.SYS_CLOSE},
	} {
		if uintptr(c.got) != c.want {
			t.Errorf("%s = %d, the kernel table says %d", c.name, c.got, c.want)
		}
	}
}
