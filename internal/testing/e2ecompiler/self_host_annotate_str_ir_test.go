package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var annotateStrCases = []struct {
	name string
	src  string
}{
	{"call_len", `function greet(n: string): string { return "hi " + n; }
function main(): i32 { let g: string = greet("bob"); return g.len(); }`},
	{"call_concat", `function pfx(n: string): string { return "x" + n; }
function main(): i32 { let g: string = pfx("ab") + pfx("cde"); return g.len(); }`},
	{"call_direct_concat", `function tag(n: string): string { return "[" + n + "]"; }
function main(): i32 { let g: string = tag("hi") + "!"; return g.len(); }`},
}

func TestSelfHostAnnotateStrIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateStrCases {
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
			progBin := buildBin(t, gcc, dir, "annstr_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
