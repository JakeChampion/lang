package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// The byte-identical self-hosting fixpoint is now proven file-based by
// TestSelfHostModloadFixpointX86_64 (compiling asm_modload_run's own
// source graph through the import-driven loader). The former marker-bundle
// TestSelfHostFixpoint — which drove the retired bundle_run.fern — was
// removed once the file-based twin covered the same mmc == gen2 == gen3
// guarantee. This file retains buildBin, the shared asm→binary linker used
// across the self-host suite.

// buildBin assembles+links asm into dir/name and returns its path. The
// (asm → static binary) link is content-addressed and cached process-
// wide (see self_host_buildcache_test.go): identical asm links once and
// later callers get a copy of the cached binary. The dir/name.s source
// is still written for callers/diagnostics that read it.
func buildBin(t *testing.T, gcc, dir, name, asm string) string {
	t.Helper()
	asmPath := filepath.Join(dir, name+".s")
	binPath := filepath.Join(dir, name)
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write %s asm: %v", name, err)
	}
	copyExecutable(t, cachedLink(t, gcc, asm), binPath)
	return binPath
}
