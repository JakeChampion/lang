package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostBumpPerRequest(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.BumpPerRequestServerSource()), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
	cmd := runX86_64Bin(cli.runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckBumpPerRequest(t, addr, e2eharness.BumpPerRequestRounds)
}
