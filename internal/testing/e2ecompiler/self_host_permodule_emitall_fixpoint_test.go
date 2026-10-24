package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostPerModuleEmitAllFixpointX86_64(t *testing.T) {
	batchUnits := pmEmitAllBatch
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProjectTyped(t)
	entry := filepath.Join(dir, "drivers/asm_modload_run.fern")

	gen0Bin := buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "eafix_gen0")

	t.Logf("gen0: emit-all of the whole compiler (batch=%d)", batchUnits)
	unitsG0 := emitAllWholeCompiler(t, runner, gen0Bin, entry, dir, "eafix_g0", "x86-64-linux", batchUnits, pmGoBuiltEmitMemoryMB)
	gen1Bin := filepath.Join(dir, "eafix_gen1")
	objsG0 := unitObjPaths(t, dir, "eafix_g0", unitsG0)
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objsG0, "-o", gen1Bin)...)
	if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link gen1 from gen0's emit-all units failed: %v\n%s", err, lout)
	}

	t.Logf("gen1: emit-all of the whole compiler (batch=%d)", batchUnits)
	unitsG1 := emitAllWholeCompiler(t, runner, gen1Bin, entry, dir, "eafix_g1", "x86-64-linux", batchUnits, pmSelfBuiltEmitMemoryMB)

	if len(unitsG1) != len(unitsG0) {
		t.Fatalf("unit count diverged: gen0 emitted %d units, gen1 emitted %d", len(unitsG0), len(unitsG1))
	}
	keys := make([]string, 0, len(unitsG0))
	for k := range unitsG0 {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	diverged := 0
	for _, k := range keys {
		u1, ok := unitsG1[k]
		if !ok {
			t.Errorf("gen1 missing unit %q that gen0 emitted", k)
			diverged++
			continue
		}
		if u1 != unitsG0[k] {
			diverged++
			if diverged <= 3 {
				t.Errorf("unit %q diverged: gen0=%d bytes, gen1=%d bytes (first diff at line %d)",
					k, len(unitsG0[k]), len(u1), firstDiffLine(unitsG0[k], u1))
			}
		}
	}
	if diverged > 0 {
		t.Fatalf("emit-all fixpoint broken: %d/%d units diverged between gen0 and gen1", diverged, len(unitsG0))
	}
	t.Logf("emit-all fixpoint holds: gen0 == gen1 across %d units, batch=%d, no OOM", len(unitsG0), batchUnits)
}

func emitAllWholeCompiler(t *testing.T, runner []string, compilerBin, entry, dir, label, target string, batchUnits, memoryMB int) map[string]string {
	t.Helper()
	build := func(args ...string) *exec.Cmd {
		full := append([]string{entry, "-target", target}, args...)
		if len(runner) == 0 {
			return exec.Command(compilerBin, full...)
		}
		return exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), compilerBin), full...)...)
	}
	drive := func(args ...string) (string, error) {
		out, err := build(args...).Output()
		return string(out), err
	}
	driveRSS := func(args ...string) (int64, error) {
		cmd := build(args...)
		_, err := cmd.Output()
		var rss int64
		if cmd.ProcessState != nil {
			if ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
				rss = ru.Maxrss
			}
		}
		return rss, err
	}

	jobs := planWholeCompilerUnits(t, drive, dir, label)
	totalUnits := len(jobs)

	outDir := filepath.Join(dir, label+"_out")
	if err := os.RemoveAll(outDir); err != nil {
		t.Fatalf("[%s] clear outDir: %v", label, err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("[%s] mkdir outDir: %v", label, err)
	}

	start := time.Now()
	batches := runPMEmitBatches(totalUnits, batchUnits, memoryMB, func(lo, hi int) (int64, error) {
		return driveRSS("-per-module-emit-all", "-out-dir", outDir,
			"-func-budget", strconv.Itoa(pmFuncBudget),
			"-unit-range", strconv.Itoa(lo)+":"+strconv.Itoa(hi))
	})
	var peakRSSKB int64
	for _, batch := range batches {
		if batch.rss > peakRSSKB {
			peakRSSKB = batch.rss
		}
		if batch.err != nil {
			hint := ""
			if ee, ok := batch.err.(*exec.ExitError); ok {
				switch ee.ExitCode() {
				case 125:
					hint = " — arena exhausted (exit 125); lower the batch size"
				case 137:
					hint = " — SIGKILLed (exit 137): the HOST ran out of RAM, not the arena; lower the concurrency or the budget knobs"
				}
			}
			t.Fatalf("[%s] emit-all batch [%d:%d]: %v%s", label, batch.lo, batch.hi, batch.err, hint)
		}
	}

	units := map[string]string{}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("[%s] read outDir: %v", label, err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "unit_") || !strings.HasSuffix(name, ".s") {
			continue
		}
		k := strings.TrimSuffix(strings.TrimPrefix(name, "unit_"), ".s")
		bts, rerr := os.ReadFile(filepath.Join(outDir, name))
		if rerr != nil {
			t.Fatalf("[%s] read unit %s: %v", label, name, rerr)
		}
		units[k] = string(bts)
	}
	if len(units) != totalUnits {
		t.Fatalf("[%s] emit-all wrote %d units, plan has %d", label, len(units), totalUnits)
	}
	t.Logf("[%s] emit-all: %d units in %d batches of <=%d, %.1fs, peak %.2f GB (arena ceiling 16 GiB)",
		label, len(units), len(batches), batchUnits, time.Since(start).Seconds(), float64(peakRSSKB)/(1024*1024))
	return units
}

const (
	pmGoBuiltEmitMemoryMB   = 5 * 1024
	pmSelfBuiltEmitMemoryMB = 5 * 1024
)

type pmEmitBatchResult struct {
	lo, hi int
	rss    int64
	err    error
}

func runPMEmitBatches(totalUnits, batchUnits, memoryMB int, emit func(lo, hi int) (int64, error)) []pmEmitBatchResult {
	var batches []pmEmitBatchResult
	for lo := 0; lo < totalUnits; lo += batchUnits {
		batches = append(batches, pmEmitBatchResult{lo: lo, hi: min(lo+batchUnits, totalUnits)})
	}
	pending := make(chan int)
	var workers sync.WaitGroup
	workerCount := min(runtime.GOMAXPROCS(0), len(batches))
	if e2eharness.InterpDriverMode() {
		workerCount = min(1, len(batches))
	}
	for range workerCount {
		workers.Go(func() {
			for i := range pending {
				batch := &batches[i]
				batch.err = withBuildMemoryMB(memoryMB, func() error {
					var err error
					batch.rss, err = emit(batch.lo, batch.hi)
					return err
				})
			}
		})
	}
	for i := range batches {
		pending <- i
	}
	close(pending)
	workers.Wait()
	return batches
}

func unitObjPaths(t *testing.T, dir, label string, units map[string]string) []string {
	t.Helper()
	keys := make([]string, 0, len(units))
	for k := range units {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var objs []string
	entryUnits := 0
	for _, k := range keys {
		if strings.Contains(units[k], "\n_start:\n") || strings.HasPrefix(units[k], "_start:\n") {
			entryUnits++
		}
		objs = append(objs, filepath.Join(dir, label+"_out", "unit_"+k+".s"))
	}
	if entryUnits != 1 {
		t.Fatalf("[%s] expected exactly one entry unit (_start), got %d", label, entryUnits)
	}
	return objs
}

func firstDiffLine(a, b string) int {
	la := strings.Split(a, "\n")
	lb := strings.Split(b, "\n")
	n := len(la)
	if len(lb) < n {
		n = len(lb)
	}
	for i := 0; i < n; i++ {
		if la[i] != lb[i] {
			return i + 1
		}
	}
	if len(la) != len(lb) {
		return n + 1
	}
	return 0
}
