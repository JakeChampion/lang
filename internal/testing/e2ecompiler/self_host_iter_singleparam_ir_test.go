package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var iterSingleParamIRCases = []struct {
	name string
	main string
}{
	{"sum", "import \"core/iter\";\nfunction main(): i32 { return iter.sum(iter.range(1, 5)); }\n"},
	{"product", "import \"core/iter\";\nfunction main(): i32 { return iter.product(iter.range(1, 5)); }\n"},
	{"min", "import \"core/iter\";\nfunction main(): i32 { match (iter.min(iter.range(3, 9))) { Some(v) => { return v; }, None => { return 0; }, } }\n"},
	{"max", "import \"core/iter\";\nfunction main(): i32 { match (iter.max(iter.range(3, 9))) { Some(v) => { return v; }, None => { return 0; }, } }\n"},
	{"contains", "import \"core/iter\";\nfunction main(): i32 { if (iter.contains(iter.range(1, 9), 5)) { return 7; } return 0; }\n"},
	{"count", "import \"core/iter\";\nfunction main(): i32 { return iter.count(iter.range(0, 5), 3); }\n"},
	{"index-of", "import \"core/iter\";\nfunction main(): i32 { match (iter.index_of(iter.range(10, 20), 13)) { Some(p) => { return p; }, None => { return 99; }, } }\n"},
	{"len", "import \"core/iter\";\nfunction main(): i32 { return iter.range(0, 7).len(); }\n"},
}

func TestSelfHostIterSingleParamIRX86_64(t *testing.T) {
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

	for _, tc := range iterSingleParamIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.main)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.main), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			decide, err := exec.Command(mmc, mainPath, stdlibRoot, "-decide").Output()
			if err != nil {
				t.Fatalf("decide: %v", err)
			}
			if got := string(bytes.TrimSpace(decide)); got != "ir" {
				t.Fatalf("%s routed %q, want ir", tc.name, got)
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
