package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostStdlibImportX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
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
		exit int
	}{
		{"std-math-leaf", "import \"std/math\";\nfunction main(): i32 {\n let r: i32[] = math.range(0, 10);\n let sum: i32 = 0;\n for x in r { sum = sum + x; }\n return sum - 3;\n}\n", 42},
		{"core-cmp-transitive", "import \"core/cmp\";\nfunction main(): i32 {\n let a: i32[] = [5, 3, 8, 1, 9, 2];\n let s: i32[] = cmp.sort(a);\n return s[0] + s[5] + 32;\n}\n", 42},
		{"std-json-intrinsics", "import \"std/json\";\nfunction main(): i32 {\n let m: Map[string,i32] = map_new(8);\n m = m.insert(\"a\", 10);\n m = m.insert(\"b\", 32);\n let r: i32 = 0;\n match (m.get(\"a\")) { Some(v) => { r = r + v; }, None => { } }\n match (m.get(\"b\")) { Some(v) => { r = r + v; }, None => { } }\n return r;\n}\n", 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.main), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asm, err := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if err != nil {
				t.Fatalf("loader compile: %v", err)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			cmd := exec.Command(progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
