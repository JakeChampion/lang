package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostMapI64ValueIRX86_64(t *testing.T) {
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

	cases := []struct {
		name string
		main string
	}{
		{"i64-literal", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i64] = Map { 1: 5000000007 }; let g: i64 = m.get_or(1, 0); return (g % 1000) as i32; }\n"},
		{"u64-cast-shift", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, u64] = Map { 1: 18000000000000000000 as u64 }; return (m.get_or(1, 0 as u64) >> 58) as i32; }\n"},
		{"i64-var-insert", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i64] = map_new(8); let v: i64 = 9000000000; m = m.insert(1, v); return (m.get_or(1, 0) % 1000) as i32; }\n"},
		{"u64-insert-shift", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, u64] = map_new(8); m = m.insert(1, 18000000000000000000 as u64); return (m.get_or(1, 0 as u64) >> 58) as i32; }\n"},
		{"i64-unannotated-getor", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i64] = Map { 2: 12000000005 }; let g = m.get_or(2, 0); return (g % 1000) as i32; }\n"},
		{"i64-default-miss", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i64] = map_new(8); let g: i64 = m.get_or(99, 7000000009); return (g % 1000) as i32; }\n"},
	}

	for _, tc := range cases {
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
