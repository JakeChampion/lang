package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var f64MethodCases = []struct {
	name string
	body string
}{
	{"sqrt", `let x: f64 = 16.0; return x.sqrt() as i32;`},
	{"floor", `let x: f64 = 7.9; return x.floor() as i32;`},
	{"ceil", `let x: f64 = 7.1; return x.ceil() as i32;`},
	{"trunc", `let x: f64 = 7.9; return x.trunc() as i32;`},
	{"round", `let x: f64 = 2.5; return x.round() as i32;`},
	{"abs", `let x: f64 = 0.0 - 5.5; return x.abs() as i32;`},
	{"pow", `let x: f64 = 2.0; return x.pow(5.0) as i32;`},
	{"exp", `let x: f64 = 2.0; return x.exp() as i32;`},
	{"log", `let x: f64 = 10.0; return x.log() as i32;`},
}

func f64MethodSrc(body string) string {
	return "import \"std/f64\";\nfunction main(): i32 { " + body + " }\n"
}

func TestSelfHostF64MethodIR_X86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "mmc")

	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range f64MethodCases {
		t.Run(tc.name, func(t *testing.T) {
			src := f64MethodSrc(tc.body)
			want := interpExit(t, interpBin, src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}

			route, derr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (import set now busts the IR budget — prune it)", tc.name, got)
			}

			asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "f64m_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
