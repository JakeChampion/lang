package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostTupleBinderShadowsModuleDeclX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	files := map[string]string{}
	for _, name := range []string{"main.fern", "shadowlib.fern"} {
		src, err := os.ReadFile(filepath.Join("../../../conformance/cases/tuple_binder_shadows_module_decl", name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(src)
	}
	asm, progDir := compileFilesModload(t, runner, driverBin, files)
	progBin := buildBin(t, gcc, progDir, "tuple_binder_shadow", asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 63 {
		t.Errorf("tuple-binder-shadow program exited %d, want 63 (bits: 1 tuple, 2 nested tuple, 4 variant in tuple, 8 payload sub-pattern, 16 match expression, 32 plain variant control; 0 = the unshadowed control lost the module function)", code)
	}
}
