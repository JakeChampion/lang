package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestServePerIPCapInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "srv.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.PerIPCapServerSource()), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", srcPath))
	e2eharness.CheckPerIPCap(t, addr)
}

func TestServePerIPCapManyPeersInterp(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the peers dial from 127.0.0.2 and up, which only Linux routes to loopback by default")
	}
	bin := buildLangBinForInterp(t)
	srcPath := filepath.Join(t.TempDir(), "srv.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.PerIPCapServerSource()), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", srcPath))
	e2eharness.CheckPerIPCapManyPeers(t, addr)
}
