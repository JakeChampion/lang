package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSelfHostAssumeEligibleByteIdenticalX86_64 is the byte-identity proof
// between the two whole-compiler emit routes, and of the `-assume-eligible`
// memory optimization (#3457 slice-3 memory blocker) that the batched route
// relies on.
//
// The per-process route (`-per-module-emit N [-func-range LO:HI]`, one unit per
// process) runs the IR-eligibility pre-check (ircore.all_eligible_view_base_range),
// which fully RE-LOWERS every function of the module purely to verify it lowers
// — a second whole-module lowering pass on top of the emit's own. On the
// self-host bump arena (no GC) the two passes stack, so the pre-check ~doubles
// the per-window peak (measured on the Go-built driver: irlower window 4.07 GB
// → 1.81 GB, util 3.05 GB → 1.75 GB). `-per-module-emit-all -assume-eligible`
// — the route every whole-compiler build in CI and the driver's own default
// build take — skips the pre-check and emits a batch of units per process
// against side-tables derived once (emit_module_ir_unit_flat).
//
// The guarantee is BYTE-IDENTITY: every unit the checked per-process route emits
// for the plan's windows must equal the unit emit-all writes for the same
// window. That establishes at once that skipping a pure verification pass
// changes no output, that sharing the once-derived side-tables changes no
// output, and that emit-all's in-driver windowing matches the harness's plan
// (planWholeCompilerUnits, the same flat order `-unit-range` batches over). The
// emit-all units are then linked into a compiler and smoke-run, so the memory
// win is free of any output or correctness change — and the per-process route,
// which nothing else in CI drives any more, stays covered on every push.
func TestSelfHostAssumeEligibleByteIdenticalX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "ae_driver")
	entry := filepath.Join(dir, "asm_modload_run.fern")

	drive := func(args ...string) (string, error) {
		full := append([]string{entry}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, full...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), full...)...)
		}
		out, err := cmd.Output()
		return string(out), err
	}

	// The whole-program runtime-need union the single-unit route folds into the
	// entry unit; emit-all derives the same roots itself.
	needsOut, err := drive("-per-module-needs")
	if err != nil {
		t.Fatalf("-per-module-needs: %v", err)
	}
	var needArgs []string
	for _, ln := range strings.Split(needsOut, "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			needArgs = append(needArgs, "-extra-need", s)
		}
	}

	jobs := planWholeCompilerUnits(t, drive, dir, "ae")

	// Per-process, WITH the pre-check, one process per planned window. Each
	// process pays the whole-program parse + side-table floor (~10 s here, the
	// same for a 0-function module as a 100-function window), so the units run
	// on a small pool: the floor is CPU-bound and a checked window peaks ~2 GB,
	// which four of them fit on the runner alongside nothing else.
	workers := runtime.NumCPU()
	if workers > 4 {
		workers = 4
	}
	units := make([]string, len(jobs))
	errs := make([]error, len(jobs))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	ppStart := time.Now()
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j *pmEmitJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			args := append([]string{"-per-module-emit", strconv.Itoa(j.modIdx)}, needArgs...)
			if !(j.lo == 0 && j.hi == j.count) {
				args = append(args, "-func-range", strconv.Itoa(j.lo)+":"+strconv.Itoa(j.hi))
			}
			unit, derr := drive(args...)
			if derr == nil && len(unit) == 0 {
				derr = fmt.Errorf("empty unit")
			}
			units[i], errs[i] = unit, derr
		}(i, j)
	}
	wg.Wait()
	checked := map[string]string{}
	for i, j := range jobs {
		if errs[i] != nil {
			t.Fatalf("per-process emit (checked) mod %d [%d:%d]: %v", j.modIdx, j.lo, j.hi, errs[i])
		}
		checked[pmUnitKey(j)] = units[i]
	}
	t.Logf("per-process (checked): %d units in %.1fs on %d workers", len(checked), time.Since(ppStart).Seconds(), workers)

	assumed := emitAllWholeCompiler(t, runner, driverBin, entry, dir, "ae", "x86-64-linux", pmEmitAllBatch)

	if len(assumed) != len(checked) {
		t.Fatalf("unit count differs: per-process %d, emit-all %d", len(checked), len(assumed))
	}
	keys := make([]string, 0, len(checked))
	for k := range checked {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ea, ok := assumed[k]
		if !ok {
			t.Fatalf("emit-all missing unit %q", k)
		}
		if ea != checked[k] {
			t.Fatalf("unit %q: -assume-eligible emit-all output diverges from the checked per-process emit (checked %d bytes, emit-all %d bytes, first diff line %d)",
				k, len(checked[k]), len(ea), firstDiffLine(checked[k], ea))
		}
	}
	t.Logf("all %d units byte-identical: checked per-process == -assume-eligible emit-all", len(checked))

	objs := unitObjPaths(t, dir, "ae", assumed)
	compilerBin := filepath.Join(dir, "ae_compiler")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", compilerBin)...)
	if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link -assume-eligible compiler: %v\n%s", err, lout)
	}
	prog := filepath.Join(dir, "ae_smoke.fern")
	if err := os.WriteFile(prog, []byte("function main(): i32 { return 7; }\n"), 0o644); err != nil {
		t.Fatalf("write smoke prog: %v", err)
	}
	var scmd *exec.Cmd
	if len(runner) == 0 {
		scmd = exec.Command(compilerBin, prog)
	} else {
		scmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), compilerBin), prog)...)
	}
	sout, serr := scmd.Output()
	if serr != nil || !strings.Contains(string(sout), "call __fn_main") {
		t.Fatalf("-assume-eligible compiler smoke run failed: %v (%d bytes asm)", serr, len(sout))
	}
	t.Logf("-assume-eligible-built compiler links and compiles a program (%d bytes asm)", len(sout))
}
