//go:build !linux

package e2ecompiler

import (
	"runtime"
	"testing"
)

func selfHostFsFacts(t *testing.T, _ string) hostFs {
	t.Helper()
	t.Skipf("the self-host statfs probe compares against statfs(2) on the host; %s runs no ELF leg", runtime.GOOS)
	return hostFs{}
}
