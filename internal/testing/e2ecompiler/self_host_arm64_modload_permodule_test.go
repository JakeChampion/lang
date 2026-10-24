package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostModloadPerModuleWholeCompilerArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProjectTyped(t)

	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_modload_run.fern", "arm64driver")
	entry := filepath.Join(dir, "drivers/asm_modload_run.fern")

	units := emitAllWholeCompiler(t, x86runner, driverBin, entry, dir, "wc_arm", "arm64-linux", pmEmitAllBatch, pmGoBuiltEmitMemoryMB)
	objs := unitObjPaths(t, dir, "wc_arm", units)

	binPath := filepath.Join(dir, "selfcompiler_pm_arm64")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", binPath)...)
	if lout, err := exec.Command(armgcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link per-module whole-compiler arm64 units failed: %v\n%s", err, lout)
	}

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

	selfNeeds, err := runArm64Bin(qemu, binPath, entry, "-target", "arm64-linux", "-per-module-needs").Output()
	if err != nil {
		t.Fatalf("per-module-built arm64 compiler OOM/crash on its own -per-module-needs (#3456): %v", err)
	}
	for _, root := range []string{"heap", "str_concat", "arr_push"} {
		if !strings.Contains(string(selfNeeds), root) {
			t.Errorf("self-driven -per-module-needs missing root %q", root)
		}
	}
}
