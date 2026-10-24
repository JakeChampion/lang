package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var annotateStructCases = []struct {
	name string
	src  string
}{
	{"call_field", `struct P { x: i32, y: i32 }
function mk(a: i32): P { return P { x: a, y: a * 2 }; }
function main(): i32 { return mk(7).x + mk(3).y; }`},
	{"call_method", `struct P { x: i32, y: i32 }
function (p: P) sum(): i32 { return p.x + p.y; }
function mk(a: i32): P { return P { x: a, y: a + 1 }; }
function main(): i32 { return mk(10).sum(); }`},
	{"call_infer_local", `struct Pt { x: i32, y: i32 }
function origin(): Pt { return Pt { x: 3, y: 4 }; }
function main(): i32 { let p = origin(); return p.x * p.y; }`},
}

func TestSelfHostAnnotateStructIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)
	for _, tc := range annotateStructCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			route, derr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR annotate path)", tc.name, got)
			}
			asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "annstruct_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
