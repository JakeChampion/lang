// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/self_host_fixpoint_test.go.
package e2eharness

import (
	"os"
	"path/filepath"
	"testing"
)

// BuildBin assembles+links asm into dir/name and returns its path. The
// (asm → static binary) link is content-addressed and cached process-
// wide (see self_host_buildcache_test.go): identical asm links once and
// later callers get a copy of the cached binary. The dir/name.s source
// is still written for callers/diagnostics that read it.
func BuildBin(t *testing.T, gcc, dir, name, asm string) string {
	t.Helper()
	asmPath := filepath.Join(dir, name+".s")
	binPath := filepath.Join(dir, name)
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write %s asm: %v", name, err)
	}
	copyExecutable(t, CachedLink(t, gcc, asm), binPath)
	return binPath
}
