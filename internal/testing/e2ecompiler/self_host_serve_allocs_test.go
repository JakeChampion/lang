package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// #9853 exit criterion A, count half: what the serve loop allocates per
// hello request on a keep-alive connection, through the self-host compiler.
// docs/NET-P0-MESSAGE-LAYER-PLAN.md §6 takes it to zero; each slice lowers
// the pin.
func TestSelfHostServeAllocsPerRequest(t *testing.T) {
	cli := buildSelfHostCLI(t)
	port := selfHostFreePort(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ServeAllocsServerSource(port)), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
	e2eharness.StartServerProcess(t, runX86_64Bin(cli.runner, bin))
	e2eharness.CheckServeAllocs(t, fmt.Sprintf("127.0.0.1:%d", port), 10)
}
