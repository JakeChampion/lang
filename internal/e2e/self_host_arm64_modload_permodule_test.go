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

// TestSelfHostModloadPerModuleWholeCompilerArm64 is the arm64 counterpart of
// TestSelfHostModloadPerModuleWholeCompilerX86_64 (per-module epic #3451 /
// #3457 step 0a): the WHOLE self-host compiler compiled per-module on arm64 —
// each module its own translation unit — then linked into one arm64 binary and
// run, under qemu, AS a compiler.
//
// It is the regression guard for the `close_needs` use-after-free that the
// arm64 per-module self-build first surfaced: `EmitState.close_needs` snapshot
// `var snap: string[] = cur.needed` aliased the needed buffer into a local
// without an alias-inc, so the function-exit dec-sweep freed a box `cur.needed`
// still referenced. The freed empty-needs box was reused for a `.rodata`
// string, so `has_need` later read those bytes as the array length and walked
// off into a NULL element → `str_eq(NULL)` → SIGSEGV — the moment the
// per-module-built compiler emitted the runtime for ANY zero-need program
// (`return 7;`). Latent on x86 (the freed box isn't immediately reused there),
// which is why the x86 whole-compiler test passed while arm64 crashed. The fix
// iterates `cur.needed` by index against a captured i32 length instead of the
// aliasing snapshot.
//
// The driver itself is built as an x86 host binary (only its OUTPUT is arm64
// asm); the emitted units are assembled+linked with the aarch64 cross gcc and
// the resulting compiler is run under qemu-aarch64.
func TestSelfHostModloadPerModuleWholeCompilerArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	x86gcc, _ := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	for _, name := range []string{"asm_arm64_ir.fern", "asm_arm64.fern", "asm_arm64_modload_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Build the arm64 driver as an x86 host binary (mirrors the fixpoint harness).
	prog, _, err := modload.Load(filepath.Join(dir, "asm_arm64_modload_run.fern"))
	if err != nil {
		t.Fatalf("modload arm64 driver: %v", err)
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
		t.Fatalf("emit driver: %v", err)
	}
	driverBin := buildBin(t, x86gcc, dir, "arm64driver", asm)

	entry := filepath.Join(dir, "asm_arm64_modload_run.fern")
	drive := func(t *testing.T, args ...string) (string, error) {
		t.Helper()
		out, err := exec.Command(driverBin, append([]string{entry}, args...)...).Output()
		return string(out), err
	}

	// 1. Module count — the whole compiler is many modules.
	countOut, err := drive(t, "-per-module-count")
	if err != nil {
		t.Fatalf("-per-module-count: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(countOut))
	if err != nil || n < 10 {
		t.Fatalf("-per-module-count = %q (n=%d), want a whole-compiler count >= 10", countOut, n)
	}

	// 2. Whole-program runtime-need union.
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

	// 3. Emit every module as its own arm64 unit (every module emitting proves
	// the per-module eligibility frontier; a bail returns 0 bytes).
	var objs []string
	sawEntry := false
	for i := 0; i < n; i++ {
		unit, err := drive(t, append([]string{"-per-module-emit", strconv.Itoa(i)}, needArgs...)...)
		if err != nil || len(unit) == 0 {
			t.Fatalf("module %d: per-module emit bailed (err=%v, %d bytes)", i, err, len(unit))
		}
		if strings.Contains(unit, "\n_start:\n") || strings.HasPrefix(unit, "_start:\n") {
			sawEntry = true
		}
		p := filepath.Join(dir, "wc_arm_unit_"+strconv.Itoa(i)+".s")
		if err := os.WriteFile(p, []byte(unit), 0o644); err != nil {
			t.Fatalf("write unit %d: %v", i, err)
		}
		objs = append(objs, p)
	}
	if !sawEntry {
		t.Fatalf("no entry unit (_start) among the %d per-module units", n)
	}

	// 4. Link all arm64 units into one compiler binary.
	binPath := filepath.Join(dir, "selfcompiler_pm_arm64")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", binPath)...)
	if lout, err := exec.Command(armgcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link per-module whole-compiler arm64 units failed: %v\n%s", err, lout)
	}

	// 5. Run the arm64 compiler under qemu on a ZERO-NEED program (`return 7;`) —
	// the exact shape that triggered the close_needs UAF (emit_runtime walking an
	// empty needs set). Must emit non-empty asm and exit 0.
	progDir := t.TempDir()
	trivArg := filepath.Join(progDir, "triv.fern")
	if err := os.WriteFile(trivArg, []byte("function main(): i32 { return 7; }\n"), 0o644); err != nil {
		t.Fatalf("write triv.fern: %v", err)
	}
	out, err := exec.Command(qemu, binPath, trivArg).Output()
	if err != nil {
		t.Fatalf("per-module-built arm64 compiler crashed on a zero-need program (the close_needs UAF): %v", err)
	}
	if len(out) == 0 || !strings.Contains(string(out), ".globl _start") {
		t.Fatalf("per-module-built arm64 compiler emitted bad asm (%d bytes) for triv", len(out))
	}

	// 6. CORRECTNESS: compile + run `add(40, 2)` → exit 42 (a parameter-taking
	// function — the same guard the x86 whole-compiler test uses).
	addArg := filepath.Join(progDir, "add.fern")
	if err := os.WriteFile(addArg,
		[]byte("function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(40, 2); }\n"), 0o644); err != nil {
		t.Fatalf("write add.fern: %v", err)
	}
	addAsm, err := exec.Command(qemu, binPath, addArg).Output()
	if err != nil || len(addAsm) == 0 {
		t.Fatalf("per-module-built arm64 compiler failed to compile add.fern: %v (%d bytes)", err, len(addAsm))
	}
	addS := filepath.Join(dir, "add_pm_arm64.s")
	if err := os.WriteFile(addS, addAsm, 0o644); err != nil {
		t.Fatalf("write add asm: %v", err)
	}
	addBin := filepath.Join(dir, "add_pm_arm64")
	if lout, err := exec.Command(armgcc, "-static", "-nostdlib", "-no-pie", addS, "-o", addBin).CombinedOutput(); err != nil {
		t.Fatalf("assemble add_pm_arm64 failed: %v\n%s", err, lout)
	}
	arun := exec.Command(qemu, addBin)
	_ = arun.Run()
	if code := arun.ProcessState.ExitCode(); code != 42 {
		t.Errorf("per-module-built arm64 compiler miscompiled add(40, 2): program exited %d, want 42", code)
	}
}
