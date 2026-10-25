package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var stdlibModloadIRCases = []struct {
	name string
	main string
}{
	{"u32-min", "import \"std/u32\";\nfunction main(): i32 { let a: u32 = 7 as u32; let b: u32 = 3 as u32; return a.min(b) as i32; }\n"},
	{"u32-clamp", "import \"std/u32\";\nfunction main(): i32 { let a: u32 = 50 as u32; return a.clamp(0 as u32, 20 as u32) as i32; }\n"},
	{"u64-max", "import \"std/u64\";\nfunction main(): i32 { let a: u64 = 100 as u64; let b: u64 = 40 as u64; return a.max(b) as i32; }\n"},
	{"i64-abs", "import \"std/i64\";\nfunction main(): i32 { let a: i64 = 0 - 17; return a.abs() as i32; }\n"},
	{"i64-gcd", "import \"std/i64\";\nfunction main(): i32 { let a: i64 = 48; let b: i64 = 36; return a.gcd(b) as i32; }\n"},
	{"i64-pow", "import \"std/i64\";\nfunction main(): i32 { let a: i64 = 2; return a.pow(6) as i32; }\n"},
	{"sort-i32-desc", "import \"core/cmp\";\nfunction main(): i32 { let a: i32[] = [3, 1, 4, 1, 5]; let s = cmp.sort_desc(a); return s[0] + s[4]; }\n"},
	{"sort-u32-asc", "import \"core/cmp\";\nfunction main(): i32 { let a: u32[] = [9 as u32, 2 as u32, 7 as u32]; let s = cmp.sort(a); return s[0] as i32; }\n"},
	{"utf8-decode", "import \"std/utf8\";\nimport \"std/string\";\nfunction main(): i32 { return \"é\".chars()[0] as i32; }\n"},
	{"utf8-chars-len", "import \"std/utf8\";\nimport \"std/string\";\nfunction main(): i32 { return \"aé😀\".chars().len() * 10 + \"aé😀\".len(); }\n"},
	{"utf8-chars-astral", "import \"std/utf8\";\nimport \"std/string\";\nfunction main(): i32 { return (\"aé😀\".chars()[2] as i32) % 1000; }\n"},
	{"utf8-count", "import \"std/utf8\";\nimport \"std/string\";\nfunction main(): i32 { return \"a€\".codepoint_count() * 10 + \"a€\".len(); }\n"},
	{"utf8-encode", "import \"std/utf8\";\nimport \"std/string\";\nfunction main(): i32 { return utf8.encode(8364 as char).len() * 10 + (utf8.encode(8364 as char).chars()[0] as i32) % 100; }\n"},
}

func TestSelfHostStdlibModloadIRX86_64(t *testing.T) {
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

	for _, tc := range stdlibModloadIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.main)
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
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
