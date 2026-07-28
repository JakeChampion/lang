package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
// That is now cheap enough to consider de-gating, but DON'T de-gate this test as
// it stands: at batch=8 the gen1 emit peaks 7909 MB against the 8 GiB arena
// (~99% of the ceiling), so it would start failing with exit-137 on the next
// compiler-source growth. batch=4 costs +36 s and buys 1.15 GB of headroom — but
// this test's batch=8 is load-bearing (it is the exact pre-`-assume-eligible`
// OOM config it exists to hold), so de-gating means ADDING a batch=4 run, not
// re-tuning this one. See docs/SELFHOST-AST-RETIREMENT.md.
func TestSelfHostPerModuleEmitAllFixpointX86_64(t *testing.T) {
	if os.Getenv("RUN_EMITALL_FIXPOINT") == "" {
		t.Skip("set RUN_EMITALL_FIXPOINT=1 to run the emit-all self-reproduction proof (~4.5 min; #3457 slice 2 / #5668)")
	}
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	entry := filepath.Join(dir, "asm_modload_run.fern")

	gen0Bin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "eafix_gen0")

	t.Log("gen0: emit-all of the whole compiler (-assume-eligible)")
	unitsG0 := emitAllWholeCompiler(t, runner, gen0Bin, entry, dir, "eafix_g0", 8)
	gen1Bin := filepath.Join(dir, "eafix_gen1")
	objsG0 := unitObjPaths(t, dir, "eafix_g0", unitsG0)
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objsG0, "-o", gen1Bin)...)
	if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link gen1 from gen0's emit-all units failed: %v\n%s", err, lout)
	}

	// gen1 (self-host-built) emit-all of the SAME source, batch=8 — the batch that
	// OOM'd before #5668. With -assume-eligible each unit's peak is ~halved, so the
	// per-process batch accumulation stays under the 8 GiB arena.
	t.Log("gen1: emit-all of the whole compiler (-assume-eligible, batch=8 — the pre-#5668 OOM config)")
	unitsG1 := emitAllWholeCompiler(t, runner, gen1Bin, entry, dir, "eafix_g1", 8)

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
	t.Logf("emit-all fixpoint holds: gen0 == gen1 across %d units, batch=8, no OOM — -assume-eligible unblocks it", len(unitsG0))
}

// emitAllWholeCompiler drives compilerBin's `-per-module-emit-all -assume-eligible`
// over the whole-compiler entry in RSS-bounded batches (a fresh process per
// batch), returning a map of "<modIdx>[_s<lo>]" → unit asm read back from the
// batch output dir. The plan (planPmEmitWindows) sizes the flat unit range only;
// the driver windows internally so -unit-range [b,hi) emits exactly jobs[b:hi].
func emitAllWholeCompiler(t *testing.T, runner []string, compilerBin, entry, dir, label string, batchUnits int) map[string]string {
	t.Helper()
	drive := func(args ...string) (string, error) {
		full := append([]string{entry}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(compilerBin, full...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), compilerBin), full...)...)
		}
		out, err := cmd.Output()
		return string(out), err
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
	for b := 0; b < totalUnits; b += batchUnits {
		hi := b + batchUnits
		if hi > totalUnits {
			hi = totalUnits
		}
		if _, derr := drive("-per-module-emit-all", "-assume-eligible", "-out-dir", outDir,
			"-func-budget", strconv.Itoa(shardThreshold),
			"-unit-range", strconv.Itoa(b)+":"+strconv.Itoa(hi)); derr != nil {
			hint := ""
			if ee, ok := derr.(*exec.ExitError); ok && ee.ExitCode() == 137 {
				hint = " — arena OOM (exit 137); -assume-eligible did not bound the batch"
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
	t.Logf("[%s] emit-all: %d units in %d batches of <=%d, %.1fs", label, len(units), batches, batchUnits, time.Since(start).Seconds())
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
