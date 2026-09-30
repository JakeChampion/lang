package e2eselfhost

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The self-host twin of TestBumpPerRequestX86_64, built as production
// builds it: through the self-host CLI with the semantic lowering
// required, so a function the lowering refused would fail the build
// rather than fall back to the AST lowering.
func TestSelfHostBumpPerRequest(t *testing.T) {
	cli := buildSelfHostCLI(t)
	port := selfHostFreePort(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.BumpPerRequestServerSource(port)), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1", "FERN_SEM_IR=1")
	e2eharness.StartServerProcess(t, runX86_64Bin(cli.runner, bin))
	e2eharness.CheckBumpPerRequest(t, fmt.Sprintf("127.0.0.1:%d", port))
}
