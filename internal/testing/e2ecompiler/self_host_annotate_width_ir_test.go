package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var annotateWidthCases = []struct {
	name string
	src  string
}{
	{"i64_call_mod", `function big(): i64 { return 5000000000; }
function main(): i32 { return (big() % 7) as i32; }`},
	{"i64_call_sum_div", `function big(): i64 { return 5000000000; }
function main(): i32 { return ((big() + big()) / 1000000000) as i32; }`},
	{"u64_call_mod", `function bu(): u64 { return 18000000000000000000 as u64; }
function main(): i32 { return (bu() % 1000000007 as u64) as i32; }`},
	{"u64_suffix_if_expr_shift", `function shifted(c: boolean, p: u64): u64 { return (if (c) { 18446744073709551615u64 } else { p }) >> 60u64; }
function main(): i32 { return shifted(true, 1u64) as i32; }`},
	{"u64_suffix_match_expr_shift", `function d(c: i32): u64 { return (match (c) { 1 => 18446744073709551615u64, _ => 0u64 }) >> 60u64; }
function main(): i32 { return d(1) as i32; }`},
}

func TestSelfHostAnnotateWidthIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateWidthCases {
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
			progBin := buildBin(t, gcc, dir, "annwidth_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
