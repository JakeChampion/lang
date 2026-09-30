package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// Streamed response bodies (#9854): a file body is produced from a Reader as
// the socket takes it, and a chunk producer's chunks go out under chunked
// transfer coding, close-delimited to an HTTP/1.0 client; on the native
// backend and the interpreter, the scenario shared with the self-host twin.
func TestServeStreamingBodyX86_64(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(path, e2eharness.StreamingBodyContent(), 0o644); err != nil {
		t.Fatal(err)
	}
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.StreamingBodyServerSource(port, path))
	startSupervisedServer(t, bin, runner)
	e2eharness.CheckStreamingBody(t, fmt.Sprintf("127.0.0.1:%d", port))
}

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
