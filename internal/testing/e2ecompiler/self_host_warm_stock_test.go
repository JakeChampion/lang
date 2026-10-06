package e2ecompiler

import (
	"fmt"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordDriverSize logs a warmed driver's size and, when
// FERN_DRIVER_SIZE_REPORT names a file, appends `driver<TAB>bytes` to it for
// scripts/ci-check-driver-sizes to compare against
// .github/selfhost-driver-sizes.txt (#6826). Every driver here is linked on its
// own account, so the measurement costs one stat.
func recordDriverSize(t *testing.T, driver, bin string) {
	t.Helper()
	fi, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("stat warmed driver %s: %v", driver, err)
	}
	t.Logf("warmed driver %s: %d bytes", driver, fi.Size())
	out := os.Getenv("FERN_DRIVER_SIZE_REPORT")
	if out == "" {
		return
	}
	f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", out, err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%s\t%d\n", driver, fi.Size()); err != nil {
		t.Fatalf("write %s: %v", out, err)
	}
}

// fullSelfHostProject copies the whole compiler tree into a fresh dir.
// Because hashSelfHostSources keys on a driver's import closure (not the whole
// dir), a driver built here hashes identically to the same driver built by a
// real test from its smaller project dir — so warming from the full set
// produces cache entries the sharded tests reproduce exactly.
func fullSelfHostProject(t *testing.T) string {
	t.Helper()
	return copySelfHostTree(t)
}

// The size report is the input to the CI size gate, so the appended line has to
// carry the real byte count in the format the checker parses (#6826). Builds no
// driver: the stat is what is under test.
func TestSelfHostDriverSizeReport(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "fake.driverbin")
	if err := os.WriteFile(bin, make([]byte, 4242), 0o755); err != nil {
		t.Fatalf("write fake driver: %v", err)
	}
	report := filepath.Join(t.TempDir(), "sizes.txt")
	t.Setenv("FERN_DRIVER_SIZE_REPORT", report)

	recordDriverSize(t, "fern.fern", bin)
	recordDriverSize(t, "drivers/wasm_ir_run.fern", bin)

	got, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	want := "fern.fern\t4242\ndrivers/wasm_ir_run.fern\t4242\n"
	if string(got) != want {
		t.Errorf("report:\n%q\nwant:\n%q", got, want)
	}

	// With the var unset the warm jobs' local equivalent writes nothing at all.
	t.Setenv("FERN_DRIVER_SIZE_REPORT", "")
	recordDriverSize(t, "fern.fern", bin)
	after, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("re-read report: %v", err)
	}
	if string(after) != want {
		t.Errorf("an unset report path must append nothing, got:\n%q", after)
	}
}

// stage2Row is the FERN_WARM_DRIVER entry for fern.fern compiled by the
// fern.fern driver — the current compiler's output for itself, the bootstrap
// chain's stage2 — recorded for the size gate and never cached: it is not a
// driver any test runs.
const stage2Row = "fern.fern/stage2"

// TestSelfHostWarmStockDriver compiles each self-host driver named in the
// comma-separated FERN_WARM_DRIVER env var (e.g. "asm_run.fern,asm_ir_run.fern")
// into the disk cache (FERN_SELFHOST_BUILD_CACHE), records each one's size
// and smoke-runs it; the entry fern.fern/stage2 (stage2Row) instead records
// the size of fern.fern as compiled by the warmed fern.fern. Two CI jobs run it (.github/workflows/test-e2e-selfhost.yml):
// `cli` builds fern.fern once for the isolated driver tests that follow in the
// same job, and `driver-sizes` builds every baselined driver for the size gate.
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
		if driver == stage2Row {
			if len(runner) != 0 {
				t.Fatalf("%s runs the fern.fern driver as a compiler, which needs a native x86-64 host", driver)
			}
			stage1 := cachedDriverBin(t, gcc, dir, "fern.fern")
			stage2 := filepath.Join(t.TempDir(), "fern_stage2")
			if err := e2eharness.CompileWithSelfHost(t, stage1, e2eharness.TargetX86_64Linux, filepath.Join(dir, "fern.fern"), stage2, e2eharness.DriverBuildWeightMB("fern.fern")); err != nil {
				t.Fatal(err)
			}
			recordDriverSize(t, driver, stage2)
			continue
		}
		bin := cachedDriverBin(t, gcc, dir, driver)
		recordDriverSize(t, driver, bin)
		// Smoke: every driver reads a program on stdin; a trivial program must
		// not crash it (exit normally, any status).
		if err := runDriverStdinExits(runner, bin, "function main(): i32 { return 0; }\n"); err != nil {
			t.Fatalf("warmed driver %s did not run: %v", driver, err)
		}
	}
}
