package e2e

import (
	"fmt"
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
	port := freeLoopbackPort(t)
	srcPath := filepath.Join(t.TempDir(), "srv.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.StreamBodiesServerSource(port)), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", srcPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start interp server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	e2eharness.CheckStreamBodiesSequential(t, fmt.Sprintf("127.0.0.1:%d", port))
}
