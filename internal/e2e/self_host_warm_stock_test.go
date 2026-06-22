package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fullSelfHostProject copies EVERY examples/self_host/*.fern into a fresh dir.
// Because hashSelfHostSources keys on a driver's import closure (not the whole
// dir), a driver built here hashes identically to the same driver built by a
// real test from its smaller project dir — so warming from the full set
// produces cache entries the sharded tests reproduce exactly.
func fullSelfHostProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ents, err := os.ReadDir("../../examples/self_host")
	if err != nil {
		t.Fatalf("read self_host dir: %v", err)
	}
	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".fern" {
			continue
		}
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", e.Name(), err)
		}
	}
	return dir
}

// TestSelfHostWarmStockDriver compiles each self-host driver named in the
// comma-separated FERN_WARM_DRIVER env var (e.g. "asm_run.fern,asm_ir_run.fern")
// into the disk cache. For every driver it populates BOTH the emitted-asm cache
// (cachedSelfHostAsm — the expensive ~50-70s Go x86-64 emit) and the linked-
// binary cache (cachedLink), so tests building the driver (buildSelfHostBin, or
// cachedSelfHostAsm + cachedLink directly) hit both the emit and the link. CI's
// parallel `warm` jobs each set FERN_WARM_DRIVER +
// FERN_SELFHOST_BUILD_CACHE and run this, off the critical path, and the test
// shards restore the result (see .github/workflows/test-e2e-selfhost.yml).
// Locally, with FERN_WARM_DRIVER unset, the test is a no-op skip.
func TestSelfHostWarmStockDriver(t *testing.T) {
	list := os.Getenv("FERN_WARM_DRIVER")
	if list == "" {
		t.Skip("FERN_WARM_DRIVER unset; nothing to warm")
	}
	gcc, runner := x86_64Tooling(t)
	dir := fullSelfHostProject(t)
	for _, driver := range strings.Split(list, ",") {
		driver = strings.TrimSpace(driver)
		if driver == "" {
			continue
		}
		asm := cachedSelfHostAsm(t, dir, driver)
		bin := cachedLink(t, gcc, asm)
		// Smoke: every driver reads a program on stdin; a trivial program must
		// not crash it (exit normally, any status).
		if err := runDriverStdinExits(runner, bin, "function main(): i32 { return 0; }\n"); err != nil {
			t.Fatalf("warmed driver %s did not run: %v", driver, err)
		}
	}
}
