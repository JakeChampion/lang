package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestServeStreamingBodyInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(path, e2eharness.StreamingBodyContent(), 0o644); err != nil {
		t.Fatal(err)
	}
	port := freeLoopbackPort(t)
	srcPath := filepath.Join(dir, "srv.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.StreamingBodyServerSource(port, path)), 0o644); err != nil {
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
	e2eharness.CheckStreamingBody(t, fmt.Sprintf("127.0.0.1:%d", port))
}
