package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

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

func TestServeBinaryBodyInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	port := freeLoopbackPort(t)
	srcPath := filepath.Join(t.TempDir(), "srv.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.BinaryBodyServerSource(port)), 0o644); err != nil {
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
	e2eharness.CheckBinaryBody(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestServeResponseFieldsInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	port := freeLoopbackPort(t)
	srcPath := filepath.Join(t.TempDir(), "srv.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.ResponseFieldsServerSource(port)), 0o644); err != nil {
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
	e2eharness.CheckResponseFields(t, fmt.Sprintf("127.0.0.1:%d", port))
}
