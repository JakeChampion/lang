package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestServeFileBodyInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "served.txt")
	if err := os.WriteFile(path, []byte(e2eharness.FileBodyContent), 0o644); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(dir, "srv.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.FileBodyServerSource(path)), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", srcPath))
	e2eharness.CheckFileBody(t, addr)
}

func TestServeBinaryBodyInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	srcPath := filepath.Join(t.TempDir(), "srv.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.BinaryBodyServerSource()), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", srcPath))
	e2eharness.CheckBinaryBody(t, addr)
}

func TestServeResponseFieldsInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	srcPath := filepath.Join(t.TempDir(), "srv.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.ResponseFieldsServerSource()), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", srcPath))
	e2eharness.CheckResponseFields(t, addr)
}
