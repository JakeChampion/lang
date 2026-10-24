package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var annotateMapCases = []struct {
	name string
	src  string
}{
	{"tuple_map_elem", `import "core/map";
function build(): Map[string, i32] { let m: Map[string, i32] = Map { }; m = m.insert("a", 10); return m; }
function main(): i32 { let t = (build(), 5); return t.0.get_or("a", 0) + t.1; }`},
	{"call_get_or", `import "core/map";
function build(): Map[string, i32] { let m: Map[string, i32] = Map { }; m = m.insert("a", 10); m = m.insert("bb", 20); return m; }
function main(): i32 { let m = build(); return m.get_or("a", 0) + m.get_or("bb", 0) + m.len(); }`},
}

func TestSelfHostAnnotateMapIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateMapCases {
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
			progBin := buildBin(t, gcc, dir, "annmap_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
