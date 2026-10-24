package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var stringCases = []struct {
	name string
	main string
	exit int
}{
	{"index-of-hit", `match ("hello world".index_of("world")) { Some(i) => { return i; }, None => { return 99; } } return 0;`, 6},
	{"index-of-miss", `match ("abc".index_of("zz")) { Some(_) => { return 1; }, None => { return 7; } } return 0;`, 7},
	{"last-index-of-hit", `match ("hello hello".last_index_of("hello")) { Some(i) => { return i; }, None => { return 99; } } return 0;`, 6},
	{"index-of-ci-hit", `match ("Hello World".index_of_ci("WORLD")) { Some(i) => { return i; }, None => { return 99; } } return 0;`, 6},
	{"trim-len", `return "  hi  ".trim().len();`, 2},
	{"to-upper", `let u: string = "abc".to_ascii_upper(); return u[0] as i32;`, 65},
	{"to-lower", `let u: string = "ABC".to_ascii_lower(); return u[0] as i32;`, 97},
	{"contains", `if ("hello".contains("ell")) { return 7; } return 0;`, 7},
	{"starts-with", `if ("hello".starts_with("he")) { return 7; } return 0;`, 7},
	{"replace-len", `return "a.b.c".replace(".", "-").len();`, 5},
	{"repeat-len", `return "ab".repeat(3).len();`, 6},
	{"split-count", `return "a,b,c".split(",").len();`, 3},
}

func stringImportSource(mainBody string) []byte {
	return []byte("import \"std/string\";\nfunction main(): i32 { " + mainBody + " }\n")
}

func TestSelfHostStringX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range stringCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, stringImportSource(tc.main), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostStringArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range stringCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, stringImportSource(tc.main), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asm, cerr := runX86_64Bin(x86runner, mmc, mainPath, stdlibRoot, "-target", "arm64-linux").Output()
			if cerr != nil {
				t.Fatalf("loader compile (arm64): %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
