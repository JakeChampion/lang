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

// TestSelfHostModloadPerModuleWholeCompilerX86_64 drives the builtins-aware
// per-module build of the WHOLE self-host compiler (#3451 — the step the epic's
// own plan calls out: "make asm_modload_run able to per-module-emit the whole
// compiler (behind a flag) and prove it links/runs, before flipping the default
// and deleting the AST emitters").
//
// asm_modload_run's per-module flags follow asm_modload_run.fern's OWN import
// graph (the whole compiler), thread the built-in TYPE layouts (builtin_view)
// into the whole-program struct view, and emit each module as its own IR
// translation unit. The units linking with no undefined symbols proves the
// whole-program runtime-need aggregation is complete; the linked binary running
// as a compiler (emitting non-empty asm, exit 0) proves the per-module emit +
// link mechanics hold at full-compiler scale.
//
// The emit uses `-per-module-emit-all` — a fresh process per BATCH of units,
// each batch deriving the whole-program side tables once and sharing them
// across its units. The one-unit-per-process shape this used to drive rebuilt
// the whole-program parse + side-table floor once per unit, 54 times over, for
// the same units. Two things it uniquely carried moved rather than went away:
// the one-unit-per-process route itself is driven every push by
// TestSelfHostAssumeEligibleByteIdenticalX86_64, and so is the per-module
// IR-eligibility frontier — every module lowering with nothing bailing — which
// that test's non-`-assume-eligible` half checks and `-assume-eligible` here
// skips by design. That the two routes emit the same bytes is
// TestSelfHostPerModuleEmitAllX86_64's proof.
//
// Step 5 carries it past the emit+link milestone to SELF-COMPILE correctness: the
// per-module-built compiler compiles the whole compiler (the fixpoint gen2 input)
// without crashing and emits a real `call __fn_main`. That self-compile first
// surfaced the string[]-struct-field `.append()` aliasing UAF in the checker
// (#3561), fixed by routing string[] field appends through the clone form.
//
// This IS the whole-compiler self-compile gate (#3457 slice 2). Every
// merged-bundle fixpoint that preceded it drove the whole compiler through the
// legacy AST emitter, and all of them are DELETED as of slice 5 — a merged
// bundle is past the 512-function IR budget, so with that emitter gone there is
// nothing left to compile one. Byte-identity of the per-module self-reproduction
// is proved by TestSelfHostPerModuleEmitAllFixpointX86_64; this test guards the
// emit+link+self-compile mechanics on every run.
func TestSelfHostModloadPerModuleWholeCompilerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)

	// Build the driver (asm_modload_run) as an x86 host binary via the native
	// toolchain, exactly as the fixpoint harness does.
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "driver")

	entry := filepath.Join(dir, "asm_modload_run.fern")

	// 1. Emit every unit of the whole compiler. The entry unit folds in the full
	// runtime-need root set the driver derives itself, so the link below still
	// checks the need aggregation end to end.
	units := emitAllWholeCompiler(t, runner, driverBin, entry, dir, "wc", "x86-64-linux", pmEmitAllBatch)
	objs := unitObjPaths(t, dir, "wc", units)

	// 2. Link all units — no undefined symbols proves the runtime-need union is
	// complete (the entry's single shared runtime covers every helper any module
	// uses).
	linkStart := time.Now()
	binPath := filepath.Join(dir, "selfcompiler_pm")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", binPath)...)
	if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link per-module whole-compiler units failed (undefined runtime symbol = needs-union gap): %v\n%s", err, lout)
	}
	t.Logf("link phase: %.1fs", time.Since(linkStart).Seconds())

	// 3. The linked binary runs as a compiler: emit non-empty asm for a trivial
	// program and exit 0.
	progDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(progDir, "triv.fern"),
		[]byte("function main(): i32 { return 7; }\n"), 0o644); err != nil {
		t.Fatalf("write triv.fern: %v", err)
	}
	var rcmd *exec.Cmd
	trivArg := filepath.Join(progDir, "triv.fern")
	if len(runner) == 0 {
		rcmd = exec.Command(binPath, trivArg)
	} else {
		rcmd = exec.Command(runner[0], append(append(runner[1:], binPath), trivArg)...)
	}
	out, err := rcmd.Output()
	if err != nil {
		t.Fatalf("per-module-built compiler failed to run on a trivial program: %v", err)
	}
	if len(out) == 0 {
		t.Fatalf("per-module-built compiler emitted 0 bytes of asm")
	}
	if !strings.Contains(string(out), ".globl _start") {
		t.Errorf("per-module-built compiler output missing `.globl _start` — does not look like an asm program")
	}

	// 4. CORRECTNESS: the per-module-built compiler must compile a program with
	// FUNCTION PARAMETERS correctly. This is the regression guard for the Perceus
	// use-after-free that the whole-compiler IR emit first surfaced: routing every
	// module through the IR path means functions like asmcore.reset_locals — which
	// `return EmitState { ...s, local_names: ln, local_types: lts }`, moving FRESH
	// local arrays (a string[] and an enum-array Ty[], both of which skip the
	// struct-literal array-field admission gate) into a returned struct — were
	// IR-emitted for the first time. The exit dec-sweep then double-freed those
	// moved locals, so the per-module-built compiler's own parse/check pipeline
	// corrupted its heap and segfaulted the moment it compiled ANY function with a
	// parameter (the param rendered "a: i32" landing in a freed string buffer). A
	// no-arg `main` slipped past it (step 3); `add(40, 2)` did not. Compiling +
	// running `add(40, 2)` proves the moved-array buffers survive (exit 42).
	if err := os.WriteFile(filepath.Join(progDir, "add.fern"),
		[]byte("function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(40, 2); }\n"), 0o644); err != nil {
		t.Fatalf("write add.fern: %v", err)
	}
	var acmd *exec.Cmd
	addArg := filepath.Join(progDir, "add.fern")
	if len(runner) == 0 {
		acmd = exec.Command(binPath, addArg)
	} else {
		acmd = exec.Command(runner[0], append(append(runner[1:], binPath), addArg)...)
	}
	addAsm, err := acmd.Output()
	if err != nil {
		t.Fatalf("per-module-built compiler failed to compile a function with parameters (the reset_locals use-after-free regression): %v", err)
	}
	if len(addAsm) == 0 {
		t.Fatal("per-module-built compiler emitted 0 bytes for add.fern")
	}
	addBin := buildBin(t, gcc, dir, "add_pm", string(addAsm))
	var arun *exec.Cmd
	if len(runner) == 0 {
		arun = exec.Command(addBin)
	} else {
		arun = exec.Command(runner[0], append(runner[1:], addBin)...)
	}
	_ = arun.Run()
	if code := arun.ProcessState.ExitCode(); code != 42 {
		t.Errorf("per-module-built compiler miscompiled add(40, 2): program exited %d, want 42", code)
	}

	// 5. SELF-COMPILE (the #3561 regression guard): the per-module-built compiler
	// compiles the WHOLE compiler — its own ~1000-function multi-module source, the
	// fixpoint gen2 input. This is the case the per-module bootstrap needs and the
	// one that first surfaced the string[]-struct-field `.append()` aliasing UAF:
	// the checker's `var actx = ctx` (StmtMatch) aliases asmcore.EmitState across a
	// match arm, and bind_local_typed's `s.local_names.append(name)` — a string[]
	// FIELD read — took the in-place consume form, corrupting the shared local_names
	// buffer (it desynced from the clone-form local_types, so local_type_of read out
	// of bounds → NULL Ty → SIGSEGV in the checker). Small programs (steps 3-4) never
	// hit it; only the whole-compiler self-compile does. Asserting it compiles + emits
	// a real `call __fn_main` (not the no-main fallback) guards the fix.
	selfCompileStart := time.Now()
	var scmd *exec.Cmd
	if len(runner) == 0 {
		scmd = exec.Command(binPath, entry)
	} else {
		scmd = exec.Command(runner[0], append(append(runner[1:], binPath), entry)...)
	}
	gen2, err := scmd.Output()
	if err != nil {
		t.Fatalf("per-module-built compiler crashed self-compiling the whole compiler (#3561 regression): %v", err)
	}
	t.Logf("whole-compiler self-compile phase: %.1fs", time.Since(selfCompileStart).Seconds())
	if !strings.Contains(string(gen2), "call __fn_main") {
		t.Errorf("self-compiled whole compiler missing `call __fn_main` — has_main misread (no-main fallback)")
	}

	// 6. SELF-DRIVEN -per-module-needs (the #3456 step toward the IR-only bootstrap):
	// the per-module-built compiler drives ITS OWN whole-program runtime-need query.
	// This used to OOM (exit 137): the old -per-module-needs ran a full emit_module_funcs
	// over every loaded module in one process to read back the exact need union, and the
	// self-host runtime does not yet reclaim the per-function IR-op allocations (#3425),
	// so a self-host-built compiler exhausted memory. It now returns the static
	// all_runtime_need_roots over-approximation (the entry emits the full runtime), so the
	// query is allocation-cheap and the self-host compiler can run it. (The full
	// self-driven per-module BUILD still needs the large-module emit OOM resolved — the
	// self-host Perceus reclamation work, roadmap goal #2 — so this guards only the needs
	// query, the slice that landed here.)
	var ncmd *exec.Cmd
	if len(runner) == 0 {
		ncmd = exec.Command(binPath, entry, "-per-module-needs")
	} else {
		ncmd = exec.Command(runner[0], append(append(runner[1:], binPath), entry, "-per-module-needs")...)
	}
	selfNeeds, err := ncmd.Output()
	if err != nil {
		t.Fatalf("per-module-built compiler OOM/crash on its own -per-module-needs (#3456 regression): %v", err)
	}
	for _, root := range []string{"heap", "str_concat", "maps", "arr_push"} {
		if !strings.Contains(string(selfNeeds), root) {
			t.Errorf("self-driven -per-module-needs missing root %q (got %q)", root, strings.TrimSpace(string(selfNeeds)))
		}
	}
}

// pmEmitAllBatch is the units-per-process batch every whole-compiler emit-all in
// this package drives. It is also what `emit_per_module_spawned` uses for the
// driver's own default build, so the harness and the compiler exercise one
// memory shape rather than two.
const pmEmitAllBatch = 8

// pmFuncBudget is the [lo,hi) function-window budget the emit plan is sized
// with, passed to the driver as -func-budget so its internal windowing matches.
//
// An oversized module (irlower, ~511 funcs) OOMs (exit 137) if emitted in one
// process: the leak-mode runtime never reclaims the per-function IR-op lists, so
// they accumulate past the arena ceiling. The #3425 fix shards such a module's
// emit by a [lo,hi) FUNCTION WINDOW — each window emits a non-entry library
// sub-unit that links exactly like a per-module unit. irlower's heaviest
// lowering functions cluster around index ~200, so peak scales with which
// functions a window holds, not just the count. 100-func windows keep the worst
// window's measured peak ~2.3 GB — comfortably clear of the kill point — while a
// coarser split (e.g. 150) can land the whole heavy cluster in one window
// (~2.6 GB).
const pmFuncBudget = 100

// pmEmitJob is one emit window of the whole-compiler build plan: module modIdx's
// functions [lo,hi) out of count.
type pmEmitJob struct {
	modIdx, lo, hi, count int
}

// pmUnitKey names the unit a job emits, the way the driver names it: the base
// module namespace for the FIRST window (lo==0), whether or not that window is
// the whole module (the "lo=0 shard keeps the base ns" invariant), and
// base + "_s<lo>" for lo>0. This matches emit-all's file naming
// (unit_<modIdx>[_s<lo>].s) and the per-process unit content.
func pmUnitKey(j *pmEmitJob) string {
	if j.lo == 0 {
		return strconv.Itoa(j.modIdx)
	}
	return strconv.Itoa(j.modIdx) + "_s" + strconv.Itoa(j.lo)
}

// planWholeCompilerUnits asks the driver for the whole-compiler module shape
// (count, post-lift function counts, staged source sizes) and expands it into
// the flat window plan both emit routes use.
func planWholeCompilerUnits(t *testing.T, drive func(...string) (string, error), dir, label string) []*pmEmitJob {
	t.Helper()
	countOut, err := drive("-per-module-count")
	if err != nil {
		t.Fatalf("[%s] -per-module-count: %v", label, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(countOut))
	if err != nil || n < 10 {
		t.Fatalf("[%s] -per-module-count = %q (n=%d), want a whole-compiler count >= 10", label, countOut, n)
	}

	// Post lift_lambdas / infer — the exact set emit_module_funcs windows over.
	fcOut, err := drive("-per-module-func-counts")
	if err != nil {
		t.Fatalf("[%s] -per-module-func-counts: %v", label, err)
	}
	var funcCounts []int
	for _, ln := range strings.Split(strings.TrimSpace(fcOut), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			c, cerr := strconv.Atoi(s)
			if cerr != nil {
				t.Fatalf("[%s] -per-module-func-counts: non-int line %q: %v", label, s, cerr)
			}
			funcCounts = append(funcCounts, c)
		}
	}
	if len(funcCounts) != n {
		t.Fatalf("[%s] -per-module-func-counts returned %d counts, want %d (module count)", label, len(funcCounts), n)
	}

	// Function count alone under-shards a module of FEW but GIANT functions:
	// asm_arm64 (32 funcs, ~470 KB — emit_runtime alone is ~235 KB) crossed the
	// arena ceiling emitted whole while staying far under the 100-func budget.
	// The manifest lines are `name|hash|hash|deps`; module sources are staged as
	// dir/<name>.fern by WriteSelfHostModloadProject.
	manOut, err := drive("-per-module-manifest")
	if err != nil {
		t.Fatalf("[%s] -per-module-manifest: %v", label, err)
	}
	var modBytes []int
	for _, ln := range strings.Split(manOut, "\n") {
		s := strings.TrimSpace(ln)
		if s == "" {
			continue
		}
		name := s
		if i := strings.IndexByte(s, '|'); i >= 0 {
			name = s[:i]
		}
		// A module without a staged file (the synthesized "__entry") weighs 0:
		// its window stays func-count based, so the entry is never sharded
		// (the exactly-one-_start link assertion depends on that).
		sz := 0
		if fi, serr := os.Stat(filepath.Join(dir, name+".fern")); serr == nil {
			sz = int(fi.Size())
		}
		modBytes = append(modBytes, sz)
	}
	if len(modBytes) != n {
		t.Fatalf("[%s] -per-module-manifest returned %d modules, want %d", label, len(modBytes), n)
	}
	return planPmEmitWindows(funcCounts, modBytes, pmFuncBudget)
}

// planPmEmitWindows expands per-module function counts + source bytes into the
// ordered window plan, module-major and window-minor — the same flat order the
// driver's own unit list uses, so -unit-range [b,hi) emits exactly jobs[b:hi].
func planPmEmitWindows(funcCounts, modBytes []int, funcBudget int) []*pmEmitJob {
	var jobs []*pmEmitJob
	for i := range funcCounts {
		window := emitWindowSize(funcCounts[i], modBytes[i], funcBudget)
		if funcCounts[i] <= window {
			jobs = append(jobs, &pmEmitJob{modIdx: i, lo: 0, hi: funcCounts[i], count: funcCounts[i]})
			continue
		}
		for lo := 0; lo < funcCounts[i]; lo += window {
			hi := lo + window
			if hi > funcCounts[i] {
				hi = funcCounts[i]
			}
			jobs = append(jobs, &pmEmitJob{modIdx: i, lo: lo, hi: hi, count: funcCounts[i]})
		}
	}
	return jobs
}

// emitWindowSize returns the [lo,hi) function-window size for a module of
// `funcs` functions and `bytes` source bytes. The function-count budget and a
// 300 KB source-byte budget each set a minimum window count; the window is
// funcs/ceil(max of the two). Byte weighting exists for modules of FEW but
// GIANT functions (asm_arm64: 32 funcs / ~470 KB) that the count threshold
// alone emits whole — measured past the bump-arena trap (exit 137).
func emitWindowSize(funcs, bytes, funcBudget int) int {
	const byteBudget = 300_000
	nWin := (funcs + funcBudget - 1) / funcBudget
	if byBytes := (bytes + byteBudget - 1) / byteBudget; byBytes > nWin {
		nWin = byBytes
	}
	if nWin < 1 {
		nWin = 1
	}
	window := (funcs + nWin - 1) / nWin
	if window < 1 {
		window = 1
	}
	return window
}
