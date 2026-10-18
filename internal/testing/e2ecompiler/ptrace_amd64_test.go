//go:build linux

package e2ecompiler

import (
	"syscall"
	"testing"
)

// syscallNumber is the syscall a tracee stopped at entry is making.
func syscallNumber(regs *syscall.PtraceRegs) uint64 {
	return regs.Orig_rax
}

// nativeBinary compiles src for the host's own ISA, for a test that traces
// the program rather than emulating it.
func (c *selfHostCLI) nativeBinary(t *testing.T, src string, env ...string) string {
	t.Helper()
	return c.x86Binary(t, src, env...)
}
