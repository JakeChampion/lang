//go:build linux && arm64

package e2ecompiler

import (
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func syscallNumber(regs *syscall.PtraceRegs) uint64 {
	return regs.Regs[8]
}

func buildNativeSelfHostCLI(t *testing.T) *selfHostCLI {
	t.Helper()
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	bin := e2eharness.BuildSelfHostBinFor(t, dir, "fern.fern", "fern", e2eharness.TargetArm64Linux)
	return &selfHostCLI{stdlib: langSrcAbs(t, "internal/stdlib"), bin: bin}
}

func (c *selfHostCLI) nativeBinary(t *testing.T, src string, env ...string) string {
	t.Helper()
	return c.arm64Binary(t, src, env...)
}
