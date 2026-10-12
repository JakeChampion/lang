package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// TestServeWebSocketSequentialInterp is the Go compiler's twin of the
// WebSocket gate (TestSelfHostServeWebSocket): under the blocking
// fallback the session's reads block the loop, so only the checks that
// drive one client at a time hold.
func TestServeWebSocketSequentialInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	srcPath := filepath.Join(t.TempDir(), "srv.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.WebSocketServerSource()), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", srcPath))
	e2eharness.CheckWebSocketSequential(t, addr)
}
