//go:build linux

package e2ecompiler

import (
	"syscall"
	"testing"
)

// syscallNumber is the syscall a tracee stopped at entry is making: x8.
func syscallNumber(regs *syscall.PtraceRegs) uint64 {
	return regs.Regs[8]
}

// nativeBinary compiles src for the host's own ISA, for a test that traces
// the program rather than emulating it.
func (c *selfHostCLI) nativeBinary(t *testing.T, src string, env ...string) string {
	t.Helper()
	return c.arm64Binary(t, src, env...)
}
