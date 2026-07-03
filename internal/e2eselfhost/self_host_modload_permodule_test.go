package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostModloadPerModuleWholeCompilerX86_64 drives the builtins-aware
// per-module build of the WHOLE self-host compiler (#3451 — the step the epic's
// own plan calls out: "make asm_modload_run able to per-module-emit the whole
// compiler (behind a flag) and prove it links/runs, before flipping the default
// and deleting the AST emitters").
//
// asm_modload_run's per-module flags (-per-module-count / -per-module-needs /
// -per-module-emit N) follow asm_modload_run.fern's OWN import graph (the whole
// compiler — ~12 modules, ~1000 funcs), thread the built-in TYPE layouts
// (builtin_view) into the whole-program struct view, and emit each module as its
// own IR translation unit. Every module emitting (no "module not IR-eligible"
// bail) proves the per-module eligibility frontier reached 12/12 (struct view +
// read_file + args slices); the units linking with no undefined symbols proves
// the whole-program runtime-need aggregation is complete; the linked binary
// running as a compiler (emitting non-empty asm, exit 0) proves the per-module
// emit + link mechanics hold at full-compiler scale.
//
// Step 7 carries it past the emit+link milestone to SELF-COMPILE correctness: the
// per-module-built compiler compiles the whole compiler (the fixpoint gen2 input)
// without crashing and emits a real `call __fn_main`. That self-compile first
// surfaced the string[]-struct-field `.append()` aliasing UAF in the checker
// (#3561), fixed by routing string[] field appends through the clone form. Routing
// the DEFAULT bootstrap (TestSelfHostModloadFixpointX86_64) through this path to the
// byte-identical fixpoint — and then deleting the AST emitters (#3457) — is the next
// slice; until then the default bootstrap stays on the merged AST emit, untouched.
func TestSelfHostModloadPerModuleWholeCompilerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)

	// Build the driver (asm_modload_run) as an x86 host binary via the native
	// toolchain, exactly as the fixpoint harness does.
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "driver")

	entry := filepath.Join(dir, "asm_modload_run.fern")

	drive := func(t *testing.T, args ...string) (string, error) {
		t.Helper()
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

	// 1. Module count — the whole compiler is many modules (>= 10).
	countOut, err := drive(t, "-per-module-count")
	if err != nil {
		t.Fatalf("-per-module-count: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(countOut))
	if err != nil || n < 10 {
		t.Fatalf("-per-module-count = %q (n=%d), want a whole-compiler count >= 10", countOut, n)
	}

	// 2. Whole-program runtime-need union (one need per line; blank lines skipped).
	needsOut, err := drive(t, "-per-module-needs")
	if err != nil {
		t.Fatalf("-per-module-needs: %v", err)
	}
	var needArgs []string
	for _, ln := range strings.Split(needsOut, "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			needArgs = append(needArgs, "-extra-need", s)
		}
	}

	// Per-module function counts (post lift_lambdas / infer — the exact set
	// emit_module_funcs windows over). An oversized module (irlower, ~511 funcs)
	// OOMs (exit 137) if emitted in one process: the leak-mode runtime never
	// reclaims the per-function IR-op lists, so they accumulate past the arena
	// ceiling. The #3425 fix shards such a module's emit by a [lo,hi) FUNCTION
	// WINDOW across separate process invocations (-func-range LO:HI) — each shard
	// emits a non-entry library sub-unit that links exactly like a per-module unit.
	fcOut, err := drive(t, "-per-module-func-counts")
	if err != nil {
		t.Fatalf("-per-module-func-counts: %v", err)
	}
	var funcCounts []int
	for _, ln := range strings.Split(strings.TrimSpace(fcOut), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			c, cerr := strconv.Atoi(s)
			if cerr != nil {
				t.Fatalf("-per-module-func-counts: non-int line %q: %v", s, cerr)
			}
			funcCounts = append(funcCounts, c)
		}
	}
	if len(funcCounts) != n {
		t.Fatalf("-per-module-func-counts returned %d counts, want %d (module count)", len(funcCounts), n)
	}

	// Any module with more than shardThreshold functions is emitted in
	// ceil(count/shardThreshold) function-index windows, keeping each emit's peak
	// heap under the OOM ceiling (#3425). Modules under the threshold emit whole
	// (full range — byte-identical to the unsharded emit). irlower (~511 funcs)
	// OOMs whole (~2.8 GB, exit 137); its heaviest lowering functions cluster
	// around index ~200, so peak scales with which functions a window holds, not
	// just the count. 100-func windows keep the worst shard's measured peak
	// ~2.3 GB — comfortably clear of the kill point — while a coarser split (e.g.
	// 150) can land the whole heavy cluster in one window (~2.6 GB). The ~1.9 GB
	// per-process floor (whole-program parse + all_funcs/all_structs + the emit
	// side-tables, rebuilt each invocation) is the irreducible base; the shard
	// window bounds only the per-function IR-op accumulation on top of it.
	const shardThreshold = 100

	// 3. Emit every module as its own unit(s) (the entry folds in the need union).
	// Every module emitting proves the 12/12 per-module eligibility frontier.
	var objs []string
	entryUnits := 0
	emitUnit := func(t *testing.T, modIdx int, tag string, rangeArgs []string) {
		t.Helper()
		emitArgs := append([]string{"-per-module-emit", strconv.Itoa(modIdx)}, needArgs...)
		emitArgs = append(emitArgs, rangeArgs...)
		unit, err := drive(t, emitArgs...)
		if err != nil || len(unit) == 0 {
			t.Fatalf("module %d%s: per-module emit bailed (err=%v, %d bytes) — a module is not IR-eligible or a shard OOMed", modIdx, tag, err, len(unit))
		}
		if strings.Contains(unit, "\n_start:\n") || strings.HasPrefix(unit, "_start:\n") {
			entryUnits++
		}
		p := filepath.Join(dir, "wc_unit_"+strconv.Itoa(modIdx)+tag+".s")
		if err := os.WriteFile(p, []byte(unit), 0o644); err != nil {
			t.Fatalf("write unit %d%s: %v", modIdx, tag, err)
		}
		objs = append(objs, p)
	}
	for i := 0; i < n; i++ {
		if funcCounts[i] <= shardThreshold {
			emitUnit(t, i, "", nil)
			continue
		}
		// Oversized module: emit in threshold-sized [lo,hi) function windows.
		for lo := 0; lo < funcCounts[i]; lo += shardThreshold {
			hi := lo + shardThreshold
			if hi > funcCounts[i] {
				hi = funcCounts[i]
			}
			rng := strconv.Itoa(lo) + ":" + strconv.Itoa(hi)
			emitUnit(t, i, "_s"+strconv.Itoa(lo), []string{"-func-range", rng})
		}
	}
	if entryUnits == 0 {
		t.Fatalf("no entry unit (_start) among the per-module units")
	}
	// Exactly one _start must exist — more than one means the entry module was
	// (incorrectly) sharded, which would collide _start at link.
	if entryUnits != 1 {
		t.Fatalf("expected exactly one entry unit (_start), got %d — was the entry module sharded?", entryUnits)
	}

	// 4. Link all units — no undefined symbols proves the runtime-need union is
	// complete (the entry's single shared runtime covers every helper any module
	// uses).
	binPath := filepath.Join(dir, "selfcompiler_pm")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", binPath)...)
	if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link per-module whole-compiler units failed (undefined runtime symbol = needs-union gap): %v\n%s", err, lout)
	}

	// 5. The linked binary runs as a compiler: emit non-empty asm for a trivial
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

	// 6. CORRECTNESS: the per-module-built compiler must compile a program with
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
	// no-arg `main` slipped past it (step 5); `add(a, b)` did not. Compiling +
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

	// 7. SELF-COMPILE (the #3561 regression guard): the per-module-built compiler
	// compiles the WHOLE compiler — its own ~1000-function multi-module source, the
	// fixpoint gen2 input. This is the case the per-module bootstrap needs and the
	// one that first surfaced the string[]-struct-field `.append()` aliasing UAF:
	// the checker's `var actx = ctx` (StmtMatch) aliases asmcore.EmitState across a
	// match arm, and bind_local_typed's `s.local_names.append(name)` — a string[]
	// FIELD read — took the in-place consume form, corrupting the shared local_names
	// buffer (it desynced from the clone-form local_types, so local_type_of read out
	// of bounds → NULL Ty → SIGSEGV in the checker). Small programs (steps 5-6) never
	// hit it; only the whole-compiler self-compile does. Asserting it compiles + emits
	// a real `call __fn_main` (not the no-main fallback) guards the fix.
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
	if !strings.Contains(string(gen2), "call __fn_main") {
		t.Errorf("self-compiled whole compiler missing `call __fn_main` — has_main misread (no-main fallback)")
	}

	// 8. SELF-DRIVEN -per-module-needs (the #3456 step toward the IR-only bootstrap):
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
