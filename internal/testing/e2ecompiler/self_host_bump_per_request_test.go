package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// #9853's per-request gate, bump half: requests on one keep-alive connection
// reuse what earlier ones freed, so the bump high-water mark stops moving.
// Built through the self-host CLI with the semantic lowering required, so a
// function the lowering refused fails the build.
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
