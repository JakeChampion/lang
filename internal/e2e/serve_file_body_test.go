package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// A file body (#9854): the handler names a file with `file`
// and the serve loop reads it as it writes the response, or answers 404
// for one it cannot read; on the native backend and the interpreter, the
// scenario shared with the self-host twin.
func TestServeFileBodyX86_64(t *testing.T) {
	path := filepath.Join(t.TempDir(), "served.txt")
	if err := os.WriteFile(path, []byte(e2eharness.FileBodyContent), 0o644); err != nil {
		t.Fatal(err)
	}
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.FileBodyServerSource(port, path))
	startSupervisedServer(t, bin, runner)
	e2eharness.CheckFileBody(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestServeFileBodyInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "served.txt")
	if err := os.WriteFile(path, []byte(e2eharness.FileBodyContent), 0o644); err != nil {
		t.Fatal(err)
	}
	port := freeLoopbackPort(t)
	srcPath := filepath.Join(dir, "srv.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.FileBodyServerSource(port, path)), 0o644); err != nil {
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
	e2eharness.CheckFileBody(t, fmt.Sprintf("127.0.0.1:%d", port))
}
