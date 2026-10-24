package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostServeSSE(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.SSEServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckSSEOverlap(t, addr)
	e2eharness.CheckSSESequential(t, addr)
}

func TestSelfHostServeSSEArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.SSEServerSource()), 0o644); err != nil {
		t.Fatal(err)
	}
	addr, _ := e2eharness.StartInheritedServer(t, runArm64Bin(qemu, cli.arm64Binary(t, src)))
	e2eharness.CheckSSEOverlap(t, addr)
	e2eharness.CheckSSESequential(t, addr)
}
