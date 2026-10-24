package e2ecompiler

import (
	"fmt"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func fullSelfHostProject(t *testing.T) string {
	t.Helper()
	return copySelfHostTree(t)
}

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

const stage2Row = "fern.fern/stage2"

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
		if err := runDriverStdinExits(runner, bin, "function main(): i32 { return 0; }\n"); err != nil {
			t.Fatalf("warmed driver %s did not run: %v", driver, err)
		}
	}
}
