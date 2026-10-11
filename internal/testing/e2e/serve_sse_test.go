package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// TestServeSSESequentialInterp is the Go compiler's twin of the
// server-sent events gate (TestSelfHostServeSSE): under the blocking
// fallback no task parks, so the producer's wait blocks the loop and no
// keep-alive comment is written, and only the checks that drive one
// client at a time hold — each stream's three events in order, and a
// request answered after them.
func TestServeSSESequentialInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	srcPath := filepath.Join(t.TempDir(), "srv.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.SSEServerSource()), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", srcPath))
	e2eharness.CheckSSESequential(t, addr)
}
