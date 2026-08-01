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

// TestSelfHostPerModuleFixpointX86_64 is Stage 1 of retiring the legacy AST
// emitters (#3457): proof that the PER-MODULE emit path is byte-fixpoint-stable
// when the emitting compiler is itself self-host-built.
//
// The existing whole-compiler per-module test
// (TestSelfHostModloadPerModuleWholeCompilerX86_64) proves the GO-built driver
// (gen0) can per-module-emit + link the whole compiler, and that the resulting
// binary self-compiles via the MERGED path. It does NOT prove that a
// self-host-BUILT compiler can itself per-module-emit the whole compiler — the
// property the default flip depends on, and the one step 8 of that test flags
// as blocked by the self-host large-module-emit OOM (the #3425 reclamation /
// roadmap goal #2). This test settles it directly:
//
//	gen0 (Go-built driver) --per-module-emit + link--> gen1 (self-host compiler)
//	gen1 --per-module-emit--> unit set U1
//	assert U1 == U0 (byte-identical): per-module emit is self-reproducing.
//
// A green result means the per-module default is a byte-stable fixpoint and the
// AST emitters can be retired on top of it. A red result (a gen1 emit that OOMs
// on a large module, or a divergent unit) pinpoints the real remaining blocker
// BEFORE any deletion — exactly what Stage 1 is for. gen1's emit runs serially
// so an arena OOM (exit 137, the real signal) is never confounded by host-RAM
// pressure from concurrent heavy emits.
func TestSelfHostPerModuleFixpointX86_64(t *testing.T) {
	// Transitional Stage-1 de-risking proof for #3457 (retire the AST emitters).
	// It runs TWO whole-compiler per-module emits (gen0 Go-built + gen1
	// self-host-built); the gen1 emit must be SERIAL (the leaky self-host runtime
	// makes concurrent large-module emits thrash a 16 GB host), so the test runs
	// ~16 min — past the 13-min CI shard timeout. It is therefore env-gated
	// (RUN_PERMODULE_FIXPOINT=1) rather than run in every CI lane. The permanent
	// per-module fixpoint guard is its cheaper sibling
	// TestSelfHostPerModuleEmitAllFixpointBatch4X86_64, which runs UNGATED; this
	// one proves the same reproduction with a self-host-BUILT compiler.
	if os.Getenv("RUN_PERMODULE_FIXPOINT") == "" {
		t.Skip("set RUN_PERMODULE_FIXPOINT=1 to run the heavy per-module self-reproduction proof (~16 min; #3457 Stage 1)")
	}
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	entry := filepath.Join(dir, "asm_modload_run.fern")

	// gen0: the Go-built driver.
	gen0Bin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "pmfix_gen0")

	// gen0 per-module-emits the whole compiler (parallel — gen0 is the memory-
	// efficient Go-built driver) into a unit set, and we link it into gen1.
	t.Log("gen0: per-module emit of the whole compiler")
	unitsG0, objsG0 := perModuleEmitWholeCompiler(t, runner, gen0Bin, entry, dir, "g0", true)
	gen1Bin := filepath.Join(dir, "pmfix_gen1")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objsG0, "-o", gen1Bin)...)
	if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link gen1 from gen0's per-module units failed: %v\n%s", err, lout)
	}

	// gen1 (self-host-built) per-module-emits the SAME whole-compiler source.
	// This is the step the existing whole-compiler test does not cover — the
	// self-host runtime emitting large modules. It runs SERIALLY: the leaky
	// self-host runtime's per-window peak is high, and three concurrent large
	// emits thrash a 16 GB host into swap (a parallel run timed out where the
	// serial run finished in ~12 min). Serial also keeps an arena OOM (exit 137,
	// the documented large-module-emit blocker) a clean signal — though the
	// 100-func/300 KB shard plan is proven to keep every window under the arena
	// ceiling, so no window OOMs.
	t.Log("gen1: per-module emit of the whole compiler (the self-host-built emit — the blocker check)")
	unitsG1, _ := perModuleEmitWholeCompiler(t, runner, gen1Bin, entry, dir, "g1", false)

	// Fixpoint: gen1's per-module unit set must be byte-identical to gen0's.
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
		t.Fatalf("per-module fixpoint broken: %d/%d units diverged between gen0 and gen1", diverged, len(unitsG0))
	}
	t.Logf("per-module fixpoint holds: gen0 == gen1 across %d units — per-module emit is self-reproducing", len(unitsG0))
}

// perModuleEmitWholeCompiler drives compilerBin's per-module flags over the
// whole-compiler entry, returning a map of "<modIdx>_<tag>" → unit asm and the
// list of written .s object paths. It mirrors the orchestration in
// TestSelfHostModloadPerModuleWholeCompilerX86_64 (count → needs → func-counts →
// manifest → planned windows → emit), reusing the shared plan helpers. When
// parallel is false the emit runs one window at a time (isolating an arena OOM
// from host-RAM pressure). The unit files are namespaced by `label` so gen0's
// and gen1's .s files never collide in the shared dir.
func perModuleEmitWholeCompiler(t *testing.T, runner []string, compilerBin, entry, dir, label string, parallel bool) (map[string]string, []string) {
	t.Helper()
	drive := func(t *testing.T, args ...string) (string, error) {
		t.Helper()
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

	countOut, err := drive(t, "-per-module-count")
	if err != nil {
		t.Fatalf("[%s] -per-module-count: %v", label, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(countOut))
	if err != nil || n < 10 {
		t.Fatalf("[%s] -per-module-count = %q (n=%d), want >= 10", label, countOut, n)
	}

	needsOut, err := drive(t, "-per-module-needs")
	if err != nil {
		t.Fatalf("[%s] -per-module-needs: %v", label, err)
	}
	var needArgs []string
	for _, ln := range strings.Split(needsOut, "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			needArgs = append(needArgs, "-extra-need", s)
		}
	}

	fcOut, err := drive(t, "-per-module-func-counts")
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
	if len(funcCounts) != n {
		t.Fatalf("[%s] func-counts returned %d, want %d", label, len(funcCounts), n)
	}

	const shardThreshold = 100
	modBytes := perModuleSourceBytes(t, drive, dir, n)

	emitStart := time.Now()
	jobs := planPmEmitWindows(funcCounts, modBytes, shardThreshold)
	runOne := func(j *pmEmitJob) {
		emitArgs := append([]string{"-per-module-emit", strconv.Itoa(j.modIdx)}, needArgs...)
		tag := ""
		if !(j.lo == 0 && j.hi == j.count) {
			emitArgs = append(emitArgs, "-func-range", strconv.Itoa(j.lo)+":"+strconv.Itoa(j.hi))
			tag = "_s" + strconv.Itoa(j.lo)
		}
		unit, err := drive(t, emitArgs...)
		if err != nil || len(unit) == 0 {
			// Surface an arena OOM (exit 137) distinctly — it is the documented
			// self-host large-module-emit blocker (goal #2 reclamation), not a
			// codegen bail.
			hint := "a module is not IR-eligible or the shard OOMed"
			if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 137 {
				hint = "arena OOM (exit 137) — the self-host large-module-emit blocker (#3425 / goal #2)"
			}
			j.err = &pmEmitError{modIdx: j.modIdx, tag: tag, bytes: len(unit), err: err, hint: hint}
			return
		}
		j.units = append(j.units, pmEmittedUnit{tag: tag, unit: unit})
	}
	if parallel {
		runPmEmitJobs(jobs, runOne)
	} else {
		for _, j := range jobs {
			runOne(j)
		}
	}

	units := map[string]string{}
	var objs []string
	entryUnits := 0
	for _, j := range jobs {
		if j.err != nil {
			t.Fatalf("[%s] per-module emit failed: %v", label, j.err)
		}
		for _, u := range j.units {
			if strings.Contains(u.unit, "\n_start:\n") || strings.HasPrefix(u.unit, "_start:\n") {
				entryUnits++
			}
			key := strconv.Itoa(j.modIdx) + u.tag
			units[key] = u.unit
			p := filepath.Join(dir, "pmfix_"+label+"_unit_"+strconv.Itoa(j.modIdx)+u.tag+".s")
			if err := os.WriteFile(p, []byte(u.unit), 0o644); err != nil {
				t.Fatalf("[%s] write unit %s: %v", label, key, err)
			}
			objs = append(objs, p)
		}
	}
	if entryUnits != 1 {
		t.Fatalf("[%s] expected exactly one entry unit (_start), got %d", label, entryUnits)
	}
	t.Logf("[%s] per-module emit: %d units in %.1fs", label, len(units), time.Since(emitStart).Seconds())
	return units, objs
}

// pmEmitError distinguishes an arena OOM from a codegen bail in the per-module
// fixpoint emit.
type pmEmitError struct {
	modIdx int
	tag    string
	bytes  int
	err    error
	hint   string
}

func (e *pmEmitError) Error() string {
	return "module " + strconv.Itoa(e.modIdx) + e.tag + ": emit failed (err=" +
		errString(e.err) + ", " + strconv.Itoa(e.bytes) + " bytes) — " + e.hint
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// firstDiffLine returns the 1-based line number of the first difference between
// a and b, or 0 if they are equal.
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
