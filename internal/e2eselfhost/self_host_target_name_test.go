package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostTargetNameFoldsOnTheModloadDriverX86_64 pins what the self-host
// answers for `target_os()` and `target_arch()` when the driver, not fern.fern,
// is doing the compiling.
//
// There is no symbol of either name to call, so before the fold the lowering
// emitted a `call_direct` to a name no module defines and `func_ineligible_reason`
// bailed the WHOLE module out of the IR path — `module not IR-eligible: interp`,
// with the bail site only visible under FERN_STRICT_IR=1. That is how
// interp.fern's use of target_os() came to red three self-host driver tests at
// once, and target_arch() lands in exactly the same place.
//
// Two halves are asserted here because the fix has two. asm_modload_run folds
// from its own -target, the way fern.fern does, so the ISA is the one asked
// for rather than the pointer width's default; and irlower answers an unfolded
// call anyway, so a driver that names no target still lowers. Compiling for
// x86-64 is what separates them: the ISA default is arm64, so a program that
// prints target_arch() and says "x86-64" can only have been folded.
func TestSelfHostTargetNameFoldsOnTheModloadDriverX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	entry := "function main(): i32 {\n" +
		"    print(target_os());\n" +
		"    print(target_arch());\n" +
		"    if (target_arch() == \"x86-64\") { return 0; }\n" +
		"    return 1;\n" +
		"}\n"

	asm, progDir := compileFilesModload(t, runner, driverBin, map[string]string{"main.fern": entry})
	if len(asm) == 0 {
		t.Fatal("driver emitted 0 bytes, so the module did not reach the IR path at all")
	}

	bin := buildBin(t, gcc, progDir, "targetname", asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	out, _ := cmd.CombinedOutput()
	got := strings.TrimRight(string(out), "\n")
	if want := "linux\nx86-64"; got != want {
		t.Errorf("target name = %q, want %q\n"+
			"arm64 for the ISA means the driver took irlower's pointer-width default "+
			"instead of folding its own -target", got, want)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 (the compiled branch on target_arch() did not take the x86-64 arm)", code)
	}
}
