package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestSelfHostPerModuleEmitAllX86_64 is the byte-identity proof between the two
// whole-compiler emit routes (#3457 slice 2).
//
// `-per-module-emit-all` emits every module's unit(s) in ONE process, deriving
// the whole-program side-table bases ONCE (compute_wp_bases) and sharing them
// across all units — as INDIVIDUAL string[] params (emit_module_ir_unit_flat),
// never a string[][] across a boundary (the slice-2 RC blocker) — instead of the
// per-process path re-deriving them per unit, which is the dominant cost of the
// one-unit-per-process route.
//
// The proof is byte-identity: emit-all's units must equal what the per-process
// route emits for the SAME window plan, with the per-process side running the
// IR-eligibility pre-check that emit-all's `-assume-eligible` skips. That
// establishes (a) sharing the once-derived bases changes no output, (b) skipping
// the pre-check changes no output, and (c) emit-all's in-driver windowing matches
// the harness's. It is what lets every whole-compiler build in CI —
// TestSelfHostModloadPerModuleWholeCompilerX86_64, its arm64 twin, and the
// emit-all fixpoint — take the batched route.
func TestSelfHostPerModuleEmitAllX86_64(t *testing.T) {
	// CI-DARK: RUN_EMITALL_CHECK — the per-process baseline is one process per
	// unit and dominates the runtime. The property it proves (the two emit
	// routes agree byte-for-byte) is a design proof rather than a per-PR
	// regression risk, and it is run by hand when either route changes; the
	// standing CI guards that a whole-compiler emit still LINKS AND RUNS are
	// TestSelfHostModloadPerModuleWholeCompilerX86_64 and its arm64 twin.
	if os.Getenv("RUN_EMITALL_CHECK") == "" {
		t.Skip("set RUN_EMITALL_CHECK=1 to run the heavy emit-all byte-identity proof (#3457 slice 2)")
	}
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "emitall_driver")
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
		if v := strings.TrimSpace(ln); v != "" {
			needArgs = append(needArgs, "-extra-need", v)
		}
	}

	jobs := planWholeCompilerUnits(t, drive, dir, "emitall")

	// 1. Per-process baseline: emit each planned window in its own process, with
	// the eligibility pre-check.
	perProc := map[string]string{}
	ppStart := time.Now()
	for _, j := range jobs {
		emitArgs := append([]string{"-per-module-emit", strconv.Itoa(j.modIdx)}, needArgs...)
		if !(j.lo == 0 && j.hi == j.count) {
			emitArgs = append(emitArgs, "-func-range", strconv.Itoa(j.lo)+":"+strconv.Itoa(j.hi))
		}
		unit, derr := drive(emitArgs...)
		if derr != nil || len(unit) == 0 {
			t.Fatalf("per-process emit mod %d [%d:%d]: %v (%d bytes)", j.modIdx, j.lo, j.hi, derr, len(unit))
		}
		perProc[pmUnitKey(j)] = unit
	}
	ppDur := time.Since(ppStart)
	t.Logf("per-process: %d units in %.1fs (%d processes, each re-deriving the side-tables)", len(perProc), ppDur.Seconds(), len(jobs))

	// 2. The batched route, exactly as every whole-compiler build in CI drives it.
	eaStart := time.Now()
	emitAll := emitAllWholeCompiler(t, runner, driverBin, entry, dir, "emitall", "x86-64-linux", pmEmitAllBatch)
	t.Logf("emit-all: %d units in %.1fs — %.1fx faster", len(emitAll), time.Since(eaStart).Seconds(), ppDur.Seconds()/time.Since(eaStart).Seconds())

	// 3. Byte-identity: emit-all's unit set must equal the per-process set.
	if len(emitAll) != len(perProc) {
		t.Fatalf("unit count differs: per-process %d, emit-all %d", len(perProc), len(emitAll))
	}
	diverged := 0
	for k, pp := range perProc {
		ea, ok := emitAll[k]
		if !ok {
			t.Errorf("emit-all missing unit %q", k)
			diverged++
			continue
		}
		if ea != pp {
			diverged++
			if diverged <= 3 {
				t.Errorf("unit %q diverged: per-process %d bytes, emit-all %d bytes (first diff line %d)",
					k, len(pp), len(ea), firstDiffLine(pp, ea))
			}
		}
	}
	if diverged > 0 {
		t.Fatalf("emit-all NOT byte-identical to per-process: %d/%d units differ", diverged, len(perProc))
	}
	t.Logf("byte-identity holds: emit-all == per-process across all %d units", len(perProc))

	// 4. The emit-all units link into a working compiler and run.
	objs := unitObjPaths(t, dir, "emitall", emitAll)
	compilerBin := filepath.Join(dir, "emitall_compiler")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", compilerBin)...)
	if lout, lerr := exec.Command(gcc, linkArgs...).CombinedOutput(); lerr != nil {
		t.Fatalf("link emit-all compiler: %v\n%s", lerr, lout)
	}
	prog := filepath.Join(dir, "smoke.fern")
	if werr := os.WriteFile(prog, []byte("function main(): i32 { return 7; }\n"), 0o644); werr != nil {
		t.Fatalf("write smoke prog: %v", werr)
	}
	var scmd *exec.Cmd
	if len(runner) == 0 {
		scmd = exec.Command(compilerBin, prog)
	} else {
		scmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), compilerBin), prog)...)
	}
	sout, serr := scmd.Output()
	if serr != nil || len(sout) == 0 {
		t.Fatalf("emit-all compiler smoke run: %v (%d bytes asm)", serr, len(sout))
	}
	if !strings.Contains(string(sout), "call __fn_main") {
		t.Fatalf("emit-all compiler emitted no `call __fn_main` (%d bytes)", len(sout))
	}
	t.Logf("emit-all compiler links and compiles a program (%d bytes asm)", len(sout))
}
