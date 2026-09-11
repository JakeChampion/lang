//go:build !linux

package e2eselfhost

import (
	"runtime"
	"testing"
)

// selfHostFsFacts has nothing to compare against off Linux: the probes below
// build ELF binaries with a cross-gcc and run them under qemu, so they are
// Linux-host tests, and the Darwin leg of the same helper is exercised by the
// Mach-O suite instead.
func selfHostFsFacts(t *testing.T, _ string) hostFs {
	t.Helper()
	t.Skipf("the self-host statfs probe compares against statfs(2) on the host; %s runs no ELF leg", runtime.GOOS)
	return hostFs{}
}
