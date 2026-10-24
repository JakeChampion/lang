package e2e

import (
	"os/exec"
	"runtime"
	"testing"
)

func x86NativeRunner(t *testing.T) []string {
	t.Helper()
	if runtime.GOARCH == "amd64" {
		return nil
	}
	if p, err := exec.LookPath("qemu-x86_64"); err == nil {
		return []string{p}
	}
	t.Skip("no qemu-x86_64 to run x86-64 binaries")
	return nil
}
