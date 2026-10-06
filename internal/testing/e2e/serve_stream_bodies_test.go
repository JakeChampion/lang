package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// TestServeStreamBodiesSequentialInterp is the Go compiler's twin of the
// streamed request body's gate (docs/NET-P3-SUSPENSION-PLAN.md §3.9,
// TestSelfHostServeStreamBodies): under the blocking fallback no task
// parks, so a handler's pull blocks the loop for its body, and only the
// checks that drive one client at a time hold — a chunked upload with a
// request pipelined behind it, a stalled body answered 408, a chunk past
// the cap answered 413, Expect: 100-continue, a client gone mid-body.
func TestServeStreamBodiesSequentialInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	srcPath := filepath.Join(t.TempDir(), "srv.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.StreamBodiesServerSource()), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", srcPath))
	e2eharness.CheckStreamBodiesSequential(t, addr)
}
