// x86NativeRunner is how a test execs an x86-64 Linux binary: directly on an
// amd64 host, under qemu-x86_64 elsewhere, and a SKIP when neither is there.
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
