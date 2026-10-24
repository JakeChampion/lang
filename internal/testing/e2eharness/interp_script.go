package e2eharness

import (
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var (
	langBinOnce sync.Once
	langBinPath string
	langBinErr  error
)

func BuildLangBinForInterp(t testing.TB) string {
	t.Helper()
	langBinOnce.Do(func() {
		dir, err := ProcessScratchDir("fern-e2e-bin")
		if err != nil {
			langBinErr = err
			return
		}
		bin := filepath.Join(dir, "fern")
		build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
		if out, err := build.CombinedOutput(); err != nil {
			langBinErr = err
			t.Logf("go build lang failed:\n%s", out)
			return
		}
		langBinPath = bin
	})
	if langBinErr != nil {
		t.Fatalf("BuildLangBinForInterp: %v", langBinErr)
	}
	return langBinPath
}
