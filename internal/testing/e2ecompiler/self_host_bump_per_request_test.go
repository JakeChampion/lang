package e2ecompiler

import (
	"fmt"
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
	port := selfHostFreePort(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.BumpPerRequestServerSource(port)), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
	e2eharness.StartServerProcess(t, runX86_64Bin(cli.runner, bin))
	e2eharness.CheckBumpPerRequest(t, fmt.Sprintf("127.0.0.1:%d", port), e2eharness.BumpPerRequestRounds)
}
