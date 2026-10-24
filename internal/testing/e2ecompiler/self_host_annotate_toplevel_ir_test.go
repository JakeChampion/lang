package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var annotateTopLevelCases = []struct {
	name string
	src  string
	want int
}{
	{"top_f64_call", `function half(): f64 { return 0.5; }
let t: f64 = half() + half() + 2.5;
return t as i32;
`, 3},
	{"top_u64_call", `function bigu(): u64 { return 18000000000000000000 as u64; }
let t: u64 = bigu() >> 40;
return t as i32;
`, 216},
	{"top_str_call", `function label(): string { return "abc"; }
let s: string = label();
return s.len();
`, 3},
	{"top_struct_call", `struct Pt { x: i32, y: i32 }
function origin(): Pt { return Pt { x: 3, y: 4 }; }
let p: Pt = origin();
return p.x * p.y;
`, 12},
	{"top_for_f64_method", `struct V { x: f64 }
function (v: V) scaled(): f64 { return v.x * 2.0; }
function mk(): V[] { return [V { x: 1.5 }, V { x: 0.375 }]; }
let vs: V[] = mk();
let t: f64 = 0.0;
for v in vs { t = t + v.scaled(); }
return t as i32;
`, 3},
}

func TestSelfHostAnnotateTopLevelIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, _ := annotateF64ProjDir(t)
	for _, tc := range annotateTopLevelCases {
		t.Run(tc.name, func(t *testing.T) {
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
				t.Fatalf("%s routed %q, want \"ir\" (a script must normalise through script_normalized before the gate)", tc.name, got)
			}
			asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if !strings.Contains(string(asm), "call __fn_main") {
				t.Fatalf("%s: emitted asm has no `call __fn_main` — the script did not lower through the IR", tc.name)
			}
			progBin := buildBin(t, gcc, dir, "anntop_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s (IR annotate path) exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
