package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var mapCoreIRCases = []struct {
	name string
	main string
}{
	{"get-or", "import \"core/map\";\nfunction main(): i32 { let m: Map[string, i32] = map_new(8); m = m.insert(\"a\", 5); m = m.insert(\"b\", 9); return m.get_or(\"b\", 0); }\n"},
	{"iter-sum", "import \"core/map\";\nfunction main(): i32 { let m: Map[string, i32] = map_new(8); m = m.insert(\"a\", 10); m = m.insert(\"b\", 20); m = m.insert(\"c\", 12); let t: i32 = 0; for k in m.keys() { t = t + m.get_or(k, 0); } return t; }\n"},
	{"grow", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i32] = map_new(2); let i: i32 = 0; while (i < 50) { m = m.insert(i, i); i = i + 1; } return m.get_or(37, 0); }\n"},
	{"str-val", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, string] = map_new(8); m = m.insert(1, \"hello\"); return m.get_or(1, \"\").len(); }\n"},
	{"cleared-unannotated", "import \"core/map\";\nfunction main(): i32 { let m: Map[string, i32] = map_new(8); m = m.insert(\"a\", 1); m = m.insert(\"b\", 2); let c = m.cleared(); return c.len() + 7; }\n"},
	{"get-unannotated", "import \"core/map\";\nfunction main(): i32 { let m: Map[string, i32] = map_new(8); m = m.insert(\"a\", 1); m = m.insert(\"b\", 2); let g = m.get(\"b\"); match (g) { Some(v) => { return v + 40; }, None => { return 9; } } }\n"},
	{"delete", "import \"core/map\";\nfunction main(): i32 { let m: Map[string, i32] = map_new(8); m = m.insert(\"x\", 5); m = m.insert(\"y\", 7); let r = m.remove(\"x\"); m = r; return m.get_or(\"x\", 99) + m.get_or(\"y\", 0); }\n"},
}

func TestSelfHostMapCoreIRX86_64(t *testing.T) {
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

	for _, tc := range mapCoreIRCases {
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
