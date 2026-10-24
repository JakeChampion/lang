package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var annotateOptCases = []struct {
	name string
	src  string
}{
	{"match_option", `function pick(n: i32): Option[i32] { if (n > 0) { return Some(n * 10); } return None; }
function main(): i32 { match (pick(4)) { Some(v) => { return v; }, None => { return 0; } } }`},
	{"try_result", `function half(n: i32): Result[i32, i32] { if (n % 2 == 0) { return Ok(n / 2); } return Err(1); }
function run(): Result[i32, i32] { let x: i32 = half(8)?; let y: i32 = half(4)?; return Ok(x + y); }
function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return 99; } } }`},
	{"match_option_string", `function name_of(n: i32): Option[string] { if (n == 1) { return Some("hello"); } return None; }
function main(): i32 { match (name_of(1)) { Some(v) => { return v.len(); }, None => { return 0; } } }`},
}

func TestSelfHostAnnotateOptIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateOptCases {
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
			progBin := buildBin(t, gcc, dir, "annopt_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
