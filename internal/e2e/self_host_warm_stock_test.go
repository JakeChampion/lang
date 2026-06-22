package e2e

import (
	"os"
	"path/filepath"
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

// TestSelfHostWarmStockDriver compiles ONE stock self-host driver (selected by
// the FERN_WARM_DRIVER env var, e.g. "asm_run.fern") into the disk cache and
// runs a trivial program through it as a smoke check. CI's parallel `warm` jobs
// each set FERN_WARM_DRIVER + FERN_SELFHOST_BUILD_CACHE and run this, so every
// dominant driver is compiled once off the critical path and the test shards
// restore it (see .github/workflows/test-e2e-selfhost.yml). Locally, with
// FERN_WARM_DRIVER unset, the test is a no-op skip.
func TestSelfHostWarmStockDriver(t *testing.T) {
	driver := os.Getenv("FERN_WARM_DRIVER")
	if driver == "" {
		t.Skip("FERN_WARM_DRIVER unset; nothing to warm")
	}
	gcc, runner := x86_64Tooling(t)
	dir := fullSelfHostProject(t)
	bin := buildSelfHostBin(t, gcc, dir, driver, "warm")

	// Smoke: every stock driver reads a program on stdin; an empty/trivial
	// program must not crash the driver (exit normally, any status).
	const prog = "function main(): i32 { return 0; }\n"
	if err := runDriverStdinExits(runner, bin, prog); err != nil {
		t.Fatalf("warmed driver %s did not run: %v", driver, err)
	}
}
