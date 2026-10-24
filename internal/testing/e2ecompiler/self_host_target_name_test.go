package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"
)

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
			"arm64 for the ISA means the driver took the lowering's pointer-width default "+
			"instead of folding its own -target", got, want)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 (the compiled branch on target_arch() did not take the x86-64 arm)", code)
	}
}
