package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostServeStreamBodies(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.StreamBodiesServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckStreamBodiesOverlap(t, addr)
	e2eharness.CheckStreamBodiesSequential(t, addr)
}

func TestSelfHostServeStreamBodiesArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.StreamBodiesServerSource()), 0o644); err != nil {
		t.Fatal(err)
	}
	addr, _ := e2eharness.StartInheritedServer(t, runArm64Bin(qemu, cli.arm64Binary(t, src)))
	e2eharness.CheckStreamBodiesOverlap(t, addr)
	e2eharness.CheckStreamBodiesSequential(t, addr)
}
