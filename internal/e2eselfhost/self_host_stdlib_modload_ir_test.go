package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// stdlibModloadIRCases exercise the self-host compiler's MODLOAD path (asm_load_run
// resolves `import "std/…"` / `core/…` transitively, then lowers the whole program)
// on real stdlib modules not covered by TestSelfHostStdlibImport. Unlike that test
// (hardcoded exits), each case here is oracle-checked against the interpreter — the
// interpreter resolves the same imports, so it's an apples-to-apples oracle for the
// multi-module program. Covers the int-method modules (std/u32, std/u64, std/i64 →
// core/int) plus more std/sort variants. x86-64 only
// (the loader driver takes argv file paths, so it can't run under the qemu runner —
// mirrors TestSelfHostStdlibImport's gate).
var stdlibModloadIRCases = []struct {
	name string
	main string
}{
	{"u32-min", "import \"std/u32\";\nfunction main(): i32 { var a: u32 = 7 as u32; var b: u32 = 3 as u32; return a.min(b) as i32; }\n"},
	{"u32-clamp", "import \"std/u32\";\nfunction main(): i32 { var a: u32 = 50 as u32; return a.clamp(0 as u32, 20 as u32) as i32; }\n"},
	{"u64-max", "import \"std/u64\";\nfunction main(): i32 { var a: u64 = 100 as u64; var b: u64 = 40 as u64; return a.max(b) as i32; }\n"},
	{"i64-abs", "import \"std/i64\";\nfunction main(): i32 { var a: i64 = 0 - 17; return a.abs() as i32; }\n"},
	{"i64-gcd", "import \"std/i64\";\nfunction main(): i32 { var a: i64 = 48; var b: i64 = 36; return a.gcd(b) as i32; }\n"},
	{"i64-pow", "import \"std/i64\";\nfunction main(): i32 { var a: i64 = 2; return a.pow(6) as i32; }\n"},
	{"sort-i32-desc", "import \"core/cmp\";\nfunction main(): i32 { var a: i32[] = [3, 1, 4, 1, 5]; var s = cmp.sort_desc(a); return s[0] + s[4]; }\n"},
	{"sort-u32-asc", "import \"core/cmp\";\nfunction main(): i32 { var a: u32[] = [9 as u32, 2 as u32, 7 as u32]; var s = cmp.sort(a); return s[0] as i32; }\n"},
	// UTF-8 codepoint layer (#4416): decode a 2-byte é to its scalar U+00E9=233.
	{"utf8-decode", "import \"std/string\";\nfunction main(): i32 { return \"é\".chars()[0] as i32; }\n"},
	// chars() counts CODEPOINTS, not bytes (#7231): "aé😀" is 3 chars over 7 bytes.
	// The self-host used to answer .chars() from a builtin returning string[], so
	// this is the fixture that pins it to std/string's decoder.
	{"utf8-chars-len", "import \"std/string\";\nfunction main(): i32 { return \"aé😀\".chars().len() * 10 + \"aé😀\".len(); }\n"},
	// The astral scalar survives intact rather than splitting into 4 byte elements.
	{"utf8-chars-astral", "import \"std/string\";\nfunction main(): i32 { return (\"aé😀\".chars()[2] as i32) % 1000; }\n"},
	// codepoint_count over a mixed-width string (a=1, €=3 bytes) is 2, not the byte len 4.
	{"utf8-count", "import \"std/string\";\nfunction main(): i32 { return \"a€\".codepoint_count() * 10 + \"a€\".len(); }\n"},
	// Encode U+20AC (€) back to its 3 UTF-8 bytes; round-trip through chars().
	{"utf8-encode", "import \"std/string\";\nfunction main(): i32 { return string.string_from_codepoint(8364 as char).len() * 10 + (string.string_from_codepoint(8364 as char).chars()[0] as i32) % 100; }\n"},
}

// TestSelfHostStdlibModloadIRX86_64 compiles each multi-module program through the
// self-hosted modload driver (asm_load_run) and checks the result against the
// interpreter oracle.
func TestSelfHostStdlibModloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
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
