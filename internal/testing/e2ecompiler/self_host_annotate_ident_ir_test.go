package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var annotateIdentCases = []struct {
	name string
	src  string
}{
	{"const_u32_shift", `const M: u32 = 2147484527u32;
function main(): i32 { return ((M >> 1u32) % 100u32) as i32; }`},
	{"const_u32_shift_bound", `const M: u32 = 2147484527u32;
function main(): i32 { let v: u32 = M >> 1u32; return (v % 100u32) as i32; }`},
	{"const_u32_shift_wide", `const M: u32 = 3221225472u32;
function main(): i32 { return ((M >> 4u32) % 100u32) as i32; }`},

	{"const_bool_to_json_true", `import "std/json";
const B: boolean = true;
function main(): i32 { return B.to_json().len() as i32; }`},
	{"const_bool_to_json_false", `import "std/json";
const B: boolean = false;
function main(): i32 { return B.to_json().len() as i32; }`},

	{"local_u32_shift", `function main(): i32 {
    let m: u32 = 2147484527u32;
    return ((m >> 1u32) % 100u32) as i32;
}`},
	{"const_u32_via_param", `const A: u32 = 2147484527u32;
function shr(x: u32): u32 { return x >> 1u32; }
function main(): i32 { return (shr(A) % 100u32) as i32; }`},

	{"const_i64_width", `const B: i64 = 5000000000i64;
function main(): i32 { return ((B + 1i64) % 100i64) as i32; }`},
	{"const_f64", `const F: f64 = 12.5;
function main(): i32 { return (F * 4.0) as i32; }`},
	{"const_string_len", `const G: string = "hello";
function main(): i32 { return G.len() as i32; }`},
}

func TestSelfHostAnnotateIdentX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range annotateIdentCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asmPath := filepath.Join(proj, "out.s")
			if out, cerr := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, mainPath, stdlibRoot).CombinedOutput(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, out)
			}
			binPath := filepath.Join(proj, "out.bin")
			if out, lerr := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); lerr != nil {
				t.Fatalf("link: %v (%s)", lerr, out)
			}
			rcmd := runX86_64Bin(runner, binPath)
			_ = rcmd.Run()
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle) — a bare name with no slot must type from ExprIdent.ty", tc.name, got, want)
			}
		})
	}
}

func TestSelfHostAnnotateIdentWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping annotate-ident wasm cases")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range annotateIdentCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			var stderr strings.Builder
			cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", "-o", outWat, mainPath, stdlibRoot)
			cmd.Stderr = &stderr
			if cerr := cmd.Run(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, stderr.String())
			}
			rcmd := exec.Command("wasmtime", "run", outWat)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle) — a bare name with no slot must type from ExprIdent.ty", tc.name, got, want)
			}
		})
	}
}
