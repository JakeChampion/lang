//go:build linux && arm64

package e2ecompiler

import (
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// syscallNumber is the syscall a tracee stopped at entry is making: x8.
func syscallNumber(regs *syscall.PtraceRegs) uint64 {
	return regs.Regs[8]
}

// buildNativeSelfHostCLI builds the self-host CLI for the host's own ISA.
// An arm64 host carries no x86 tooling, so the CLI is built for arm64 and
// run directly, as TestSelfHostCtGateArm64 does.
func buildNativeSelfHostCLI(t *testing.T) *selfHostCLI {
	t.Helper()
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	bin := e2eharness.BuildSelfHostBinFor(t, dir, "fern.fern", "fern", e2eharness.TargetArm64Linux)
	return &selfHostCLI{stdlib: langSrcAbs(t, "internal/stdlib"), bin: bin}
}

// nativeBinary compiles src for the host's own ISA, for a test that traces
// the program rather than emulating it.
func (c *selfHostCLI) nativeBinary(t *testing.T, src string, env ...string) string {
	t.Helper()
	return c.arm64Binary(t, src, env...)
}
