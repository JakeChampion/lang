package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
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
// This is the emit+link MILESTONE, not yet the correctness one: routing the
// bootstrap through this path to the BYTE-IDENTICAL fixpoint (and then deleting
// the AST emitters, #3457) is the next slice — a self-build emit-correctness bug
// (the per-module-built compiler's has_main currently misreads, so it emits the
// no-main fallback) is tracked separately. Until that lands the default bootstrap
// (TestSelfHostModloadFixpointX86_64) stays on the merged AST emit, untouched.
func TestSelfHostModloadPerModuleWholeCompilerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)

	// Build the driver (asm_modload_run) as an x86 host binary via the native
	// toolchain, exactly as the fixpoint harness does.
	prog, _, err := modload.Load(filepath.Join(dir, "asm_modload_run.fern"))
	if err != nil {
		t.Fatalf("modload driver: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverBin := buildBin(t, gcc, dir, "driver", asm)

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

	// 3. Emit every module as its own unit (the entry folds in the need union).
	// Every module emitting proves the 12/12 per-module eligibility frontier.
	var objs []string
	sawEntry := false
	for i := 0; i < n; i++ {
		emitArgs := append([]string{"-per-module-emit", strconv.Itoa(i)}, needArgs...)
		unit, err := drive(t, emitArgs...)
		if err != nil || len(unit) == 0 {
			t.Fatalf("module %d: per-module emit bailed (err=%v, %d bytes) — a module is not IR-eligible", i, err, len(unit))
		}
		if strings.Contains(unit, "\n_start:\n") || strings.HasPrefix(unit, "_start:\n") {
			sawEntry = true
		}
		p := filepath.Join(dir, "wc_unit_"+strconv.Itoa(i)+".s")
		if err := os.WriteFile(p, []byte(unit), 0o644); err != nil {
			t.Fatalf("write unit %d: %v", i, err)
		}
		objs = append(objs, p)
	}
	if !sawEntry {
		t.Fatalf("no entry unit (_start) among the %d per-module units", n)
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
	// program and exit 0. (Output CORRECTNESS — the byte-identical fixpoint — is
	// the next slice; see the doc comment.)
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
}
