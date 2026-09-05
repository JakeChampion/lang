package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)

	// Build the arm64 driver as an x86 host binary (mirrors the fixpoint harness).
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_modload_run.fern", "arm64driver")
	entry := filepath.Join(dir, "asm_modload_run.fern")

	// 1. Emit every unit of the whole compiler as arm64 asm, batched — the same
	// route (and the same batch size) `emit_per_module_spawned` drives for arm64
	// in step 5 below. The per-module eligibility frontier is target-independent
	// (module_ir_eligible takes no target) and is checked on every push by
	// TestSelfHostAssumeEligibleByteIdenticalX86_64, so nothing here needs to pay
	// for the pre-check a second time.
	units := emitAllWholeCompiler(t, x86runner, driverBin, entry, dir, "wc_arm", "arm64-linux", pmEmitAllBatch)
	objs := unitObjPaths(t, dir, "wc_arm", units)

	// 2. Link all arm64 units into one compiler binary.
	binPath := filepath.Join(dir, "selfcompiler_pm_arm64")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", binPath)...)
	if lout, err := exec.Command(armgcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link per-module whole-compiler arm64 units failed: %v\n%s", err, lout)
	}

	// 3. Run the arm64 compiler under qemu on a ZERO-NEED program (`return 7;`) —
	// the exact shape that triggered the close_needs UAF (emit_runtime walking an
	// empty needs set). Must emit non-empty asm and exit 0.
	progDir := t.TempDir()
	trivArg := filepath.Join(progDir, "triv.fern")
	if err := os.WriteFile(trivArg, []byte("function main(): i32 { return 7; }\n"), 0o644); err != nil {
		t.Fatalf("write triv.fern: %v", err)
	}
	out, err := runArm64Bin(qemu, binPath, trivArg, "-target", "arm64-linux").Output()
	if err != nil {
		t.Fatalf("per-module-built arm64 compiler crashed on a zero-need program (the close_needs UAF): %v", err)
	}
	if len(out) == 0 || !strings.Contains(string(out), ".globl _start") {
		t.Fatalf("per-module-built arm64 compiler emitted bad asm (%d bytes) for triv", len(out))
	}

	// 4. CORRECTNESS: compile + run `add(40, 2)` → exit 42 (a parameter-taking
	// function — the same guard the x86 whole-compiler test uses).
	addArg := filepath.Join(progDir, "add.fern")
	if err := os.WriteFile(addArg,
		[]byte("function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(40, 2); }\n"), 0o644); err != nil {
		t.Fatalf("write add.fern: %v", err)
	}
	addAsm, err := runArm64Bin(qemu, binPath, addArg, "-target", "arm64-linux").Output()
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
	arun := runArm64Bin(qemu, addBin)
	_ = arun.Run()
	if code := arun.ProcessState.ExitCode(); code != 42 {
		t.Errorf("per-module-built arm64 compiler miscompiled add(40, 2): program exited %d, want 42", code)
	}

	// 5. WHOLE-COMPILER MERGED-DEFAULT COMPILE (arm64). No per-module flags, so
	// the driver takes the merged route: past the 512-function IR budget, past
	// the single-process concat ceiling, and therefore out through the BATCHED
	// per-module emit (emit_per_module_spawned), which the driver orchestrates
	// itself with fork/exec rather than being driven from the harness. This is
	// the direct guard on the arm64 wiring #3457 slice 5 added; arm64 has no AST
	// emitter to drop to.
	//
	// Run on the HOST driver, not the qemu one. The per-module-BUILT compiler
	// doing this same self-compile is what the step used to assert; with the AST
	// fallback gone it now forks ~35 emit children, and under qemu that took the
	// whole test from 297 s past the 18-minute shard timeout. Its unique value
	// was the #3561 string[]-field `.append()` UAF guard, whose fix is in SHARED
	// irlower.fern and which the x86 twin exercises on every run — so what is
	// lost here is a duplicate, while what is gained is coverage of code that
	// otherwise had none.
	gen2, err := runX86_64Bin(x86runner, driverBin, entry, "-target", "arm64-linux").Output()
	if err != nil {
		t.Fatalf("arm64 whole-compiler merged compile failed (batched per-module escape): %v", err)
	}
	if !strings.Contains(string(gen2), "bl __fn_main") && !strings.Contains(string(gen2), "call __fn_main") {
		t.Errorf("whole-compiler compile missing the main call — has_main misread (no-main fallback)")
	}
	if len(gen2) == 0 {
		t.Error("arm64 whole-compiler merged compile emitted 0 bytes")
	}

	// 6. SELF-DRIVEN -per-module-needs (#3456, arm64 twin): the per-module-built arm64
	// compiler drives its own whole-program runtime-need query without OOM, now that it
	// returns the static all_runtime_need_roots over-approximation instead of re-emitting
	// every module (see the x86 twin for the mechanism).
	selfNeeds, err := runArm64Bin(qemu, binPath, entry, "-target", "arm64-linux", "-per-module-needs").Output()
	if err != nil {
		t.Fatalf("per-module-built arm64 compiler OOM/crash on its own -per-module-needs (#3456): %v", err)
	}
	for _, root := range []string{"heap", "str_concat", "maps", "arr_push"} {
		if !strings.Contains(string(selfNeeds), root) {
			t.Errorf("self-driven -per-module-needs missing root %q", root)
		}
	}
}
