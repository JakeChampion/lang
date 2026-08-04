package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSelfHostPerModuleEmitAllFixpointX86_64 is the payoff proof for the
// `-assume-eligible` memory fix (#5668): a gen0 → link → gen1 → gen1-emit-all
// byte-identity fixpoint on the whole compiler, running gen1's emit-all in
// batches of 8 units per process — the exact configuration that OOM'd (exit 137)
// BEFORE the fix.
//
// The pre-fix story (recorded then deferred in docs/SELFHOST-AST-RETIREMENT.md):
// emit-all runs a whole batch in ONE process, and each unit did TWO full
// whole-module lowering passes (the eligibility pre-check + the emit). On the
// self-host bump arena (no GC) those stack, so a unit peaked ~7.6 GB and a batch
// of 8 marched the 8 GiB arena to exit-137 on the second batch. `-assume-eligible`
// drops the redundant pre-check, ~halving each unit's arena advance, so the same
// batch of 8 now fits. This test is that A/B made permanent: same batch size,
// with `-assume-eligible`, must run green AND byte-identically (gen0 == gen1).
//
// Two whole-compiler emit-all passes. Measured 2026-07-28 at **273 s** — the
// "~12 min" this comment used to claim predates the param_is_borrowable no-alloc
// fix, which cut the per-unit emit peak ~3x and the wall with it.
//
// This test's batch=8 is load-bearing — it IS the pre-`-assume-eligible` OOM
// config — and it now runs UNGATED, in its own CI job (emitall-fixpoint-x86_64).
//
// It used to be env-gated on RUN_EMITALL_FIXPOINT, which NOTHING set, on the
// stated grounds that "the gen1 emit peaks 7909 MB against the 8 GiB arena
// (~99% of the ceiling)". The arena has been **16 GiB** for some time
// (x86_64.go's heapBytes and arm64.go's, both 0x400000000), so that reasoning
// was off by 2x in the direction that retires a test. Re-measured 2026-08-03 on
// this tree: gen0 42.0 s / 4.96 GB, gen1 108.8 s / 7.27 GB, 297.6 s total —
// **~45% of the ceiling**, with 8.7 GB of headroom rather than 0.2 GB. The
// config that was supposedly one commit away from exit-137 has room for the
// compiler sources to nearly double.
func TestSelfHostPerModuleEmitAllFixpointX86_64(t *testing.T) {
	runEmitAllFixpoint(t, 8, "eafix8")
}

// TestSelfHostPerModuleEmitAllFixpointBatch4X86_64 is the same proof at the batch
// size that has headroom, and it runs UNGATED — the standing guard that the
// whole-compiler per-module emit still reproduces itself byte-for-byte.
//
// That guard is what slices 3 and 5 rest on: repointing the driver at the
// per-module path and then DELETING the legacy AST emitters is only safe while
// something proves a self-host-built compiler emits the same units the Go-built
// one does. Gated, that proof only existed when someone remembered to run it.
//
// batch=4 rather than the sibling's 8 was chosen when the arena was 8 GiB and
// 7909 MB looked like the edge of it. Against today's 16 GiB ceiling both fit
// comfortably (batch=8 re-measured at 7.27 GB, ~45%), so the sibling is no
// longer gated — this one stays at batch=4 as the in-shard guard, and batch=8
// runs in its own job where its ~5 min does not lengthen a shard.
//
// WHAT THIS DOES NOT COVER. This is the emit-ALL fixpoint: one process emits
// every unit. It is NOT the PER-MODULE fixpoint
// (TestSelfHostPerModuleFixpointX86_64), which drives 33 separate per-module
// emits and is the only guard that exercises the per-module windowing end to
// end.
//
// That one is still env-gated (RUN_PERMODULE_FIXPOINT=1) because it runs
// ~1050 s, past the 13-minute timeout on the sharded `test` job — but it now
// has its OWN CI job (permodule-fixpoint-x86_64 in test-e2e-selfhost.yml,
// timeout 45m), which sets the variable. The shard timeout never bound a
// dedicated job, so the gate had been costing the coverage for no reason.
//
// Run it by hand when iterating locally on retain/release placement, drop
// insertion or reuse:
//
//	RUN_PERMODULE_FIXPOINT=1 go test ./internal/e2eselfhost/ \
//	    -run TestSelfHostPerModuleFixpointX86_64 -timeout 60m
func TestSelfHostPerModuleEmitAllFixpointBatch4X86_64(t *testing.T) {
	runEmitAllFixpoint(t, 4, "eafix4")
}

// runEmitAllFixpoint drives one gen0 → link → gen1 → gen1-emit-all byte-identity
// round at the given per-process batch size. Both generations use the same batch
// so the comparison isolates the COMPILER, not the windowing.
func runEmitAllFixpoint(t *testing.T, batchUnits int, label string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	entry := filepath.Join(dir, "asm_modload_run.fern")

	gen0Bin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", label+"_gen0")

	t.Logf("gen0: emit-all of the whole compiler (-assume-eligible, batch=%d)", batchUnits)
	unitsG0 := emitAllWholeCompiler(t, runner, gen0Bin, entry, dir, label+"_g0", batchUnits)
	gen1Bin := filepath.Join(dir, label+"_gen1")
	objsG0 := unitObjPaths(t, dir, label+"_g0", unitsG0)
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objsG0, "-o", gen1Bin)...)
	if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link gen1 from gen0's emit-all units failed: %v\n%s", err, lout)
	}

	// gen1 (self-host-built) emit-all of the SAME source. With -assume-eligible
	// each unit's peak is ~halved, so the per-process batch accumulation stays
	// under the 8 GiB arena.
	t.Logf("gen1: emit-all of the whole compiler (-assume-eligible, batch=%d)", batchUnits)
	unitsG1 := emitAllWholeCompiler(t, runner, gen1Bin, entry, dir, label+"_g1", batchUnits)

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

// emitAllWholeCompiler drives compilerBin's `-per-module-emit-all -assume-eligible`
// over the whole-compiler entry in RSS-bounded batches (a fresh process per
// batch), returning a map of "<modIdx>[_s<lo>]" → unit asm read back from the
// batch output dir. The plan (planPmEmitWindows) sizes the flat unit range only;
// the driver windows internally so -unit-range [b,hi) emits exactly jobs[b:hi].
func emitAllWholeCompiler(t *testing.T, runner []string, compilerBin, entry, dir, label string, batchUnits int) map[string]string {
	t.Helper()
	build := func(args ...string) *exec.Cmd {
		full := append([]string{entry}, args...)
		if len(runner) == 0 {
			return exec.Command(compilerBin, full...)
		}
		return exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), compilerBin), full...)...)
	}
	drive := func(args ...string) (string, error) {
		out, err := build(args...).Output()
		return string(out), err
	}
	// driveRSS runs a batch and returns the child's peak RSS (ru_maxrss, KB on
	// Linux). batch=4's gen1 peak sits ~1.4 GB under the fixed 8 GiB arena, so
	// logging it turns a future arena-growth regression into a visible creep in
	// the CI output instead of a silent jump to exit-137.
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

	countOut, err := drive("-per-module-count")
	if err != nil {
		t.Fatalf("[%s] -per-module-count: %v", label, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(countOut))
	if err != nil || n < 10 {
		t.Fatalf("[%s] -per-module-count = %q (n=%d), want >= 10", label, countOut, n)
	}

	fcOut, err := drive("-per-module-func-counts")
	if err != nil {
		t.Fatalf("[%s] -per-module-func-counts: %v", label, err)
	}
	var funcCounts []int
	for _, ln := range strings.Split(strings.TrimSpace(fcOut), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			c, cerr := strconv.Atoi(s)
			if cerr != nil {
				t.Fatalf("[%s] -per-module-func-counts non-int %q: %v", label, s, cerr)
			}
			funcCounts = append(funcCounts, c)
		}
	}

	const shardThreshold = 100
	modBytes := perModuleSourceBytes(t, func(_ *testing.T, args ...string) (string, error) { return drive(args...) }, dir, n)
	jobs := planPmEmitWindows(funcCounts, modBytes, shardThreshold)
	totalUnits := len(jobs)

	outDir := filepath.Join(dir, label+"_out")
	if err := os.RemoveAll(outDir); err != nil {
		t.Fatalf("[%s] clear outDir: %v", label, err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("[%s] mkdir outDir: %v", label, err)
	}

	start := time.Now()
	batches := 0
	var peakRSSKB int64
	for b := 0; b < totalUnits; b += batchUnits {
		hi := b + batchUnits
		if hi > totalUnits {
			hi = totalUnits
		}
		rss, derr := driveRSS("-per-module-emit-all", "-assume-eligible", "-out-dir", outDir,
			"-func-budget", strconv.Itoa(shardThreshold),
			"-unit-range", strconv.Itoa(b)+":"+strconv.Itoa(hi))
		if rss > peakRSSKB {
			peakRSSKB = rss
		}
		if derr != nil {
			// The two memory failures need opposite responses, and until the arena
			// trap got its own status both arrived as 137 and this hint had to
			// guess. 125 is the emitted binary exhausting its own fixed arena —
			// reproducible, a real bound-the-batch bug. 137 is 128+9, the HOST
			// kernel OOM-killing the process — infra, retry with a smaller budget.
			hint := ""
			if ee, ok := derr.(*exec.ExitError); ok {
				switch ee.ExitCode() {
				case 125:
					hint = " — arena exhausted (exit 125); -assume-eligible did not bound the batch"
				case 137:
					hint = " — SIGKILLed (exit 137): the HOST ran out of RAM, not the arena; lower the concurrency or the budget knobs"
				}
			}
			t.Fatalf("[%s] emit-all batch [%d:%d]: %v%s", label, b, hi, derr, hint)
		}
		batches++
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
		label, len(units), batches, batchUnits, time.Since(start).Seconds(), float64(peakRSSKB)/(1024*1024))
	return units
}

// unitObjPaths returns the on-disk .s paths for an emit-all unit set in
// deterministic key order (a Go map's iteration order is randomised and object
// order affects the linked image). It also asserts exactly one entry unit.
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
