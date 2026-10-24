package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSelfHostModloadPerModuleWholeCompilerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProjectTyped(t)

	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "driver")

	entry := filepath.Join(dir, "drivers/asm_modload_run.fern")

	units := emitAllWholeCompiler(t, runner, driverBin, entry, dir, "wc", "x86-64-linux", pmEmitAllBatch, pmGoBuiltEmitMemoryMB)
	objs := unitObjPaths(t, dir, "wc", units)

	linkStart := time.Now()
	binPath := filepath.Join(dir, "selfcompiler_pm")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", binPath)...)
	if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link per-module whole-compiler units failed (undefined runtime symbol = needs-union gap): %v\n%s", err, lout)
	}
	t.Logf("link phase: %.1fs", time.Since(linkStart).Seconds())

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
	for _, root := range []string{"heap", "str_concat", "arr_push"} {
		if !strings.Contains(string(selfNeeds), root) {
			t.Errorf("self-driven -per-module-needs missing root %q (got %q)", root, strings.TrimSpace(string(selfNeeds)))
		}
	}
}

const pmEmitAllBatch = 64

const pmFuncBudget = 100

type pmEmitJob struct {
	modIdx, lo, hi, count int
}

func pmUnitKey(j *pmEmitJob) string {
	if j.lo == 0 {
		return strconv.Itoa(j.modIdx)
	}
	return strconv.Itoa(j.modIdx) + "_s" + strconv.Itoa(j.lo)
}

func planWholeCompilerUnits(t *testing.T, drive func(...string) (string, error), dir, label string) []*pmEmitJob {
	t.Helper()
	shapeOut, err := drive("-per-module-shape")
	if err != nil {
		t.Fatalf("[%s] -per-module-shape: %v", label, err)
	}
	shape, err := parsePMModuleShape(shapeOut)
	if err != nil {
		t.Fatalf("[%s] -per-module-shape: %v", label, err)
	}
	if len(shape) < 10 {
		t.Fatalf("[%s] -per-module-shape returned %d modules, want a whole-compiler count >= 10", label, len(shape))
	}
	funcCounts := make([]int, len(shape))
	modBytes := make([]int, len(shape))
	for i, module := range shape {
		funcCounts[i] = module.functions
		if fi, serr := os.Stat(filepath.Join(dir, module.namespace+".fern")); serr == nil {
			modBytes[i] = int(fi.Size())
		}
	}
	return planPmEmitWindows(funcCounts, modBytes, pmEntryIndex(shape), pmFuncBudget)
}

func pmEntryIndex(shape []pmModuleShape) int {
	for i, m := range shape {
		if m.namespace == "__entry" {
			return i
		}
	}
	return -1
}

type pmModuleShape struct {
	namespace string
	functions int
}

func parsePMModuleShape(out string) ([]pmModuleShape, error) {
	var modules []pmModuleShape
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		namespace, count, ok := strings.Cut(line, "|")
		n, err := strconv.Atoi(count)
		if !ok || namespace == "" || err != nil || n < 0 {
			return nil, fmt.Errorf("invalid module shape row %q", line)
		}
		if seen[namespace] {
			return nil, fmt.Errorf("duplicate module namespace %q", namespace)
		}
		seen[namespace] = true
		modules = append(modules, pmModuleShape{namespace: namespace, functions: n})
	}
	return modules, nil
}

func planPmEmitWindows(funcCounts, modBytes []int, entry, funcBudget int) []*pmEmitJob {
	var jobs []*pmEmitJob
	for i := range funcCounts {
		window := emitWindowSize(funcCounts[i], modBytes[i], funcBudget)
		if funcCounts[i] <= window || i == entry {
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
