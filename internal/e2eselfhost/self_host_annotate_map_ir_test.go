package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// annotateMapCases extend the typed-IR annotation (#5531) to map-valued calls.
// type_to_irtag now serialises a TypeMap to its "Map[K, V]" tag (irlower's own
// spelling), and expr_map_type_tag's ExprCall arm reads it instead of
// re-deriving via the map_ret_fns registry — the decisive path being a
// map-valued call in a TUPLE element (its #3317 arm), where a later
// `t.0.get_or(...)` must recover the map's K/V. Oracle: the interpreter.
//
// Maps allocate a heap; the binary runs via the X86_64Tooling runner prefix
// (nil on an x86_64 host) so it works in a cross-arch container too.
var annotateMapCases = []struct {
	name string
	src  string
}{
	// map-valued call as a tuple element, then get_or on t.0.
	{"tuple_map_elem", `import "core/map";
function build(): Map[string, i32] { var m: Map[string, i32] = Map { }; m = m.insert("a", 10); return m; }
function main(): i32 { var t = (build(), 5); return t.0.get_or("a", 0) + t.1; }`}, // 10 + 5 = 15
	// map-valued call used directly for get_or (two lookups + len).
	{"call_get_or", `import "core/map";
function build(): Map[string, i32] { var m: Map[string, i32] = Map { }; m = m.insert("a", 10); m = m.insert("bb", 20); return m; }
function main(): i32 { var m = build(); return m.get_or("a", 0) + m.get_or("bb", 0) + m.len(); }`}, // 10 + 20 + 2 = 32
}

// TestSelfHostAnnotateMapIR_X86_64 pins the checker-stamped Map[K,V] result type
// feeding irlower's expr_map_type_tag through the IR path (#5531).
func TestSelfHostAnnotateMapIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, interpBin := annotateF64ProjDir(t)
	_, runner := x86_64Tooling(t)

	for _, tc := range annotateMapCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			route, derr := exec.Command(mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR annotate path)", tc.name, got)
			}
			asm, cerr := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "annmap_"+tc.name, string(asm))
			argv := append(append([]string{}, runner...), progBin)
			cmd := exec.Command(argv[0], argv[1:]...)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
