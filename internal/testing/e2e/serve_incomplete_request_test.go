package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The interpreter reads a request and the end of stream behind it in one
// read, the case that answered nothing before #11649: an incomplete
// request at the end of stream is answered 400.
func TestServeIncompleteRequestAtEOFInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	port := freeLoopbackPort(t)
	srcPath := filepath.Join(t.TempDir(), "srv.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.RecvDeadlineServerSource(port)), 0o644); err != nil {
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
	e2eharness.CheckIncompleteRequestAtEOF(t, fmt.Sprintf("127.0.0.1:%d", port))
}
