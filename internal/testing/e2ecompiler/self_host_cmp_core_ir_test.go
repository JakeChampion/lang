package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var cmpCoreIRCases = []struct {
	name string
	main string
}{
	{"min-max", "import \"core/cmp\";\nfunction main(): i32 { return cmp.min(3, 7) + cmp.max(3, 7); }\n"},
	{"clamp", "import \"core/cmp\";\nfunction main(): i32 { return cmp.clamp(9, 1, 5); }\n"},
	{"lt-gt", "import \"core/cmp\";\nfunction main(): i32 { if (cmp.lt(2, 3) && !cmp.gt(2, 3)) { return 1; } return 0; }\n"},
	{"sort-asc", "import \"core/cmp\";\nfunction main(): i32 { let s = cmp.sort([3, 1, 2]); return s[0] * 10 + s[2]; }\n"},
	{"sort-desc", "import \"core/cmp\";\nfunction main(): i32 { let s = cmp.sort_desc([3, 1, 2]); return s[0] * 10 + s[2]; }\n"},
}

func TestSelfHostCmpCoreIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range cmpCoreIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.main)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.main), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			probe, err := exec.Command(mmc, mainPath, stdlibRoot, "-ir-probe").Output()
			if err != nil {
				t.Fatalf("ir-probe: %v", err)
			}
			if !bytes.Contains(probe, []byte("module: IR")) {
				t.Fatalf("%s did not route module: IR\n%s", tc.name, probe)
			}
			asm, err := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("loader compile: %v", err)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			cmd := exec.Command(progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
