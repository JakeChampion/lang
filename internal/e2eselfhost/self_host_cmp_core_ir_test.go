package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// cmpCoreIRCases are real `import "core/cmp"` programs exercising the
// bounded-generic free functions min/max/clamp/lt/gt (each `[T: Ord]`,
// bodies calling `a.cmp(b)` on the type param). These are the templates
// #4374 is about: monomorphise stamps a concrete instantiation per call
// and DROPS the raw template (native does the same), so the program the
// emitter routes on has no type-param-receiver call left to bail — it
// reaches `module: IR`.
//
// Before #4374 the `-ir-probe` diagnostic scanned the RAW merged module
// (pre-monomorphise), where the un-instantiated templates were still
// present and bailed, so it mis-reported `module: AST` for a program that
// `-decide` and the actual emit route through IR. The probe now runs the
// same module_with_builtins pipeline the emitter does, so its verdict
// matches reality — this test would fail (`module: AST`) against the old
// probe and passes now.
var cmpCoreIRCases = []struct {
	name string
	main string
}{
	// min(3,7)+max(3,7) = 3+7 = 10.
	{"min-max", "import \"core/cmp\";\nfunction main(): i32 { return cmp.min(3, 7) + cmp.max(3, 7); }\n"},
	// clamp(9,1,5) = 5.
	{"clamp", "import \"core/cmp\";\nfunction main(): i32 { return cmp.clamp(9, 1, 5); }\n"},
	// lt(2,3) true, gt(2,3) false -> 1.
	{"lt-gt", "import \"core/cmp\";\nfunction main(): i32 { if (cmp.lt(2, 3) && !cmp.gt(2, 3)) { return 1; } return 0; }\n"},
	// Generic ascending sort (the fresh-copy `sort[T: Ord]`, #5397): [3,1,2] ->
	// [1,2,3]; s[0]*10 + s[2] = 13. Exercises the real core/cmp merge body
	// through the multi-module loader on the IR path.
	{"sort-asc", "import \"core/cmp\";\nfunction main(): i32 { var s = cmp.sort([3, 1, 2]); return s[0] * 10 + s[2]; }\n"},
	// Generic descending sort (`sort_desc[T: Ord]`, #5397): [3,1,2] -> [3,2,1];
	// s[0]*10 + s[2] = 31.
	{"sort-desc", "import \"core/cmp\";\nfunction main(): i32 { var s = cmp.sort_desc([3, 1, 2]); return s[0] * 10 + s[2]; }\n"},
}

// TestSelfHostCmpCoreIRX86_64 compiles real `import "core/cmp"` programs
// through the multi-module bundling loader and asserts the whole program
// routes the IR path (`module: IR`) — the regression guard for #4374's
// probe-fidelity fix — with each binary oracle-checked against the
// interpreter. x86-64 only (the loader driver takes argv file paths).
func TestSelfHostCmpCoreIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "flatten.fern", "checker.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range cmpCoreIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.main)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.main), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			// Routing assertion: the whole program must reach the IR path
			// (pre-#4374 this reported module: AST from the phantom template bail).
			probe, err := exec.Command(mmc, mainPath, stdlibRoot, "-ir-probe").Output()
			if err != nil {
				t.Fatalf("ir-probe: %v", err)
			}
			if !bytes.Contains(probe, []byte("module: IR")) {
				t.Fatalf("%s did not route module: IR\n%s", tc.name, probe)
			}
			// Compile + run, oracle-checked.
			asm, err := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("loader compile: %v", err)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			cmd := exec.Command(progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
