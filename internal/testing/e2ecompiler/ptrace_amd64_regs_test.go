//go:build linux && amd64

package e2ecompiler

import (
	"syscall"
	"testing"
)

func syscallNumber(regs *syscall.PtraceRegs) uint64 {
	return regs.Orig_rax
}

func buildNativeSelfHostCLI(t *testing.T) *selfHostCLI {
	t.Helper()
	return buildSelfHostCLI(t)
}

func (c *selfHostCLI) nativeBinary(t *testing.T, src string, env ...string) string {
	t.Helper()
	return c.x86Binary(t, src, env...)
}
