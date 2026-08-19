package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// mapCoreIRCases are real `import "core/map"` programs that exercise the core/map
// hashmap implementation (insert / get_or / keys-iter / grow / delete / string
// values). With all three core/map lowering stages in (stage 1 __alloc +
// __ptr_width, stage 2 __memset + __free, stage 3 the __fern_arr_dec /
// __fern_drop_arr_ptr no-op value-release helpers), all 32 core/map functions
// route IR, so a map program reaches "module: IR" through the bundling driver —
// it no longer bails. Each case asserts the
// modload -ir-probe verdict is "module: IR" AND the compiled binary matches the
// interpreter oracle. x86-64 only (the loader driver takes argv file paths, like
// the other modload tests). Results are <= 126.
var mapCoreIRCases = []struct {
	name string
	main string
}{
	// insert + get_or. get_or("b") = 9.
	{"get-or", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert(\"a\", 5); m = m.insert(\"b\", 9); return m.get_or(\"b\", 0); }\n"},
	// keys-iter sum. 10 + 20 + 12 = 42.
	{"iter-sum", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert(\"a\", 10); m = m.insert(\"b\", 20); m = m.insert(\"c\", 12); var t: i32 = 0; for k in m.keys() { t = t + m.get_or(k, 0); } return t; }\n"},
	// growth across many inserts (rehash). get_or(37) = 37.
	{"grow", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, i32] = map_new(2); var i: i32 = 0; while (i < 50) { m = m.insert(i, i); i = i + 1; } return m.get_or(37, 0); }\n"},
	// string-valued map. get_or(1, \"\").len() = 5.
	{"str-val", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32, string] = map_new(8); m = m.insert(1, \"hello\"); return m.get_or(1, \"\").len(); }\n"},
	// delete via without (returns (Map, removed)). get_or(\"x\",99)+get_or(\"y\",0) = 99+7 = 106.
	{"delete", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert(\"x\", 5); m = m.insert(\"y\", 7); var r = m.without(\"x\"); m = r.0; return m.get_or(\"x\", 99) + m.get_or(\"y\", 0); }\n"},
}

func TestSelfHostMapCoreIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range mapCoreIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.main)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.main), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			// Routing assertion: the whole program must reach the IR path.
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
