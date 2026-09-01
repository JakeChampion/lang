// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/interp_script_test.go.
package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// `BuildLangBinForInterp` returns a path to a compiled
// `cmd/fern` binary. Earlier this rebuilt per-call into
// `t.TempDir()`; with ~60+ callsites across the runner-
// example gates that meant 60 sequential `go build`s on
// every run. Now the binary is built **once** per `go
// test` process via `sync.Once` and shared across all
// callers — drops the per-call cost to a single map
// lookup and unblocks `t.Parallel()` in the dependent
// tests (parallel runs no longer race on temp-dir
// cleanup of a binary another test is mid-exec).
//
// The shared binary lives in `os.MkdirTemp` rather than
// `t.TempDir()`: per-test temp dirs get auto-cleaned at
// the END of THEIR test, which would yank the binary out
// from under a parallel sibling that's still using it.
// The package-level temp dir survives until the test
// process exits; OS-level temp cleanup handles the rest.
var (
	langBinOnce sync.Once
	langBinPath string
	langBinErr  error
)

func BuildLangBinForInterp(t testing.TB) string {
	t.Helper()
	langBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fern-e2e-bin-")
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
